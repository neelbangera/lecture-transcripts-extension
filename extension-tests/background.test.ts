import { describe, expect, it } from "vitest";

import {
  BackgroundCoordinator,
  DRAIN_ALARM_NAME,
  DRAIN_PERIOD_MINUTES,
  type BackgroundResponse,
} from "../extension/src/background";
import {
  ExtensionStorage,
  MAX_OVERFLOW_NOTICES,
  type StorageAreaLike,
} from "../extension/src/extension-storage";
import {
  MAX_STATUS_PAGE,
  NativeMessagingClient,
  NativeMessagingError,
  type AckResponse,
  type CommandResultResponse,
  type NativeTranscriptJob,
  type SubmitAckStatus,
} from "../extension/src/native-messaging";
import {
  EMPTY_COUNTS,
  type DrainState,
  type UploaderStatus,
} from "../extension/src/status";
import { createTranscriptJob } from "../extension/src/transcript-job";

const CAPTURED_AT = "2026-09-20T11:00:00Z";
const NOW_MS = Date.parse("2026-09-20T12:00:00Z");
const TRANSCRIPT =
  "A sufficiently long stable transcript body for the background coordinator test.";
const LECCAP_URL =
  "https://leccap.engin.umich.edu/leccap/player/r/lecture-1";
const EXTENSION_ID = "lecture-transcripts-extension-id";

interface FakeSender {
  id?: string;
  url?: string;
  tab?: { id?: number; url?: string };
}

const CONTENT_SENDER: FakeSender = { id: EXTENSION_ID, url: LECCAP_URL };
const POPUP_SENDER: FakeSender = { id: EXTENSION_ID };

function jobFor(
  lectureNumber: number,
  transcript = TRANSCRIPT,
): NativeTranscriptJob {
  return createTranscriptJob({
    courseName: "EECS 484",
    term: "Fall 2026",
    lectureNumber,
    lectureDate: "2026-09-01",
    sourceUrl: `https://leccap.engin.umich.edu/leccap/player/r/lecture-${lectureNumber}`,
    capturedAt: CAPTURED_AT,
    transcript,
  });
}

function status(overrides: Partial<UploaderStatus> = {}): UploaderStatus {
  return {
    type: "status",
    protocolVersion: 1,
    requestId: null,
    extensionVersion: "0.1.0",
    uploaderVersion: "0.2.0",
    authState: "connected",
    authorization: {
      userCode: null,
      verificationUri: null,
      verificationUriComplete: null,
      expiresAt: null,
    },
    drainState: "idle",
    counts: { ...EMPTY_COUNTS },
    jobs: [],
    nextBeforeJobId: null,
    ...overrides,
  };
}

function ackFor(
  job: NativeTranscriptJob,
  statusValue: SubmitAckStatus,
  overrides: Partial<AckResponse> = {},
): AckResponse {
  return {
    type: "ack",
    protocolVersion: 1,
    requestId: "request-submit-1",
    operation: "submit",
    jobId: statusValue === "queued" ? 1 : null,
    lectureKey: job.lectureKey,
    contentHash: job.contentHash,
    status: statusValue,
    existingStatus: null,
    action: null,
    ...overrides,
  };
}

function commandResult(
  overrides: Partial<CommandResultResponse> &
    Pick<CommandResultResponse, "operation" | "result">,
): CommandResultResponse {
  return {
    type: "command_result",
    protocolVersion: 1,
    requestId: "request-command-1",
    jobId: null,
    status: null,
    errorCategory: null,
    ...overrides,
  };
}

class MemoryStorageArea implements StorageAreaLike {
  readonly values = new Map<string, unknown>();
  readonly operations: string[] = [];

  seed(items: Record<string, unknown>): void {
    for (const [key, value] of Object.entries(items)) {
      this.values.set(key, structuredClone(value));
    }
  }

  async get(keys?: string | string[] | null): Promise<Record<string, unknown>> {
    this.operations.push("get");
    const requested =
      keys == null
        ? [...this.values.keys()]
        : Array.isArray(keys)
          ? keys
          : [keys];
    const result: Record<string, unknown> = {};
    for (const key of requested) {
      if (this.values.has(key)) {
        result[key] = structuredClone(this.values.get(key));
      }
    }
    return result;
  }

  async set(items: Record<string, unknown>): Promise<void> {
    this.operations.push("set");
    for (const [key, value] of Object.entries(items)) {
      this.values.set(key, structuredClone(value));
    }
  }
}

class FakeEvent<T extends (...args: any[]) => void> {
  private readonly listeners = new Set<T>();

  addListener(listener: T): void {
    this.listeners.add(listener);
  }

  removeListener(listener: T): void {
    this.listeners.delete(listener);
  }

  get size(): number {
    return this.listeners.size;
  }

  emit(...args: Parameters<T>): void {
    for (const listener of [...this.listeners]) {
      listener(...args);
    }
  }
}

type RuntimeMessageListener = (
  message: unknown,
  sender: FakeSender,
  sendResponse: (response: unknown) => void,
) => boolean | void;

class FakeRuntime {
  id = EXTENSION_ID;
  version = "0.1.0";
  readonly onMessage = new FakeEvent<RuntimeMessageListener>();
  readonly onInstalled = new FakeEvent<() => void>();
  readonly onStartup = new FakeEvent<() => void>();
  getManifest = (): { version?: string } => ({ version: this.version });
}

class FakeAlarms {
  readonly createCalls: Array<{
    name: string;
    info: { periodInMinutes: number };
  }> = [];
  readonly onAlarm = new FakeEvent<(alarm: { name: string }) => void>();

  async create(name: string, info: { periodInMinutes: number }): Promise<void> {
    this.createCalls.push({ name, info });
  }
}

class FakeNativeMessagingClient {
  hostAvailable = true;
  connectStatus = status();
  statusPage = status();
  submitResponses: AckResponse[] = [];
  retryResponse = commandResult({
    operation: "retry",
    result: "accepted",
    status: "queued",
  });
  discardResponse = commandResult({
    operation: "discard",
    result: "accepted",
    status: "discarded",
  });
  resetResponse = commandResult({
    operation: "reset",
    result: "accepted",
    status: "reset",
  });
  onSubmit: ((job: NativeTranscriptJob) => void | Promise<void>) | null = null;

  readonly connectCalls: string[] = [];
  readonly statusCalls: Array<{ beforeJobId?: number; limit: number }> = [];
  readonly submitCalls: NativeTranscriptJob[] = [];
  readonly retryCalls: number[] = [];
  readonly discardCalls: number[] = [];
  resetCalls = 0;
  ensureConnectedCalls = 0;
  disconnectCount = 0;
  private connected = false;
  private readonly statusListeners = new Set<(value: UploaderStatus) => void>();
  private readonly disconnectListeners = new Set<() => void>();

  onStatus(listener: (value: UploaderStatus) => void): () => void {
    this.statusListeners.add(listener);
    return () => this.statusListeners.delete(listener);
  }

  onDisconnect(listener: () => void): () => void {
    this.disconnectListeners.add(listener);
    return () => this.disconnectListeners.delete(listener);
  }

  isConnected(): boolean {
    return this.connected;
  }

  async ensureConnected(): Promise<unknown> {
    this.ensureConnectedCalls += 1;
    this.assertAvailable();
    this.connected = true;
    return {};
  }

  async connect(extensionVersion: string): Promise<UploaderStatus> {
    this.connectCalls.push(extensionVersion);
    this.assertAvailable();
    return this.connectStatus;
  }

  async submitJob(job: NativeTranscriptJob): Promise<AckResponse> {
    this.submitCalls.push(job);
    this.assertAvailable();
    if (this.onSubmit) await this.onSubmit(job);
    return this.submitResponses.shift() ?? ackFor(job, "queued");
  }

  async statusRequest(
    beforeJobId?: number,
    limit = MAX_STATUS_PAGE,
  ): Promise<UploaderStatus> {
    this.statusCalls.push({ beforeJobId, limit });
    this.assertAvailable();
    return this.statusPage;
  }

  async retryJob(jobId: number): Promise<CommandResultResponse> {
    this.retryCalls.push(jobId);
    this.assertAvailable();
    return this.retryResponse;
  }

  async discardJob(jobId: number): Promise<CommandResultResponse> {
    this.discardCalls.push(jobId);
    this.assertAvailable();
    return this.discardResponse;
  }

  async reset(): Promise<CommandResultResponse> {
    this.resetCalls += 1;
    this.assertAvailable();
    return this.resetResponse;
  }

  disconnect(): void {
    this.disconnectCount += 1;
    this.connected = false;
  }

  emitStatus(value: UploaderStatus): void {
    for (const listener of [...this.statusListeners]) listener(value);
  }

  emitDisconnect(): void {
    this.connected = false;
    for (const listener of [...this.disconnectListeners]) listener();
  }

  private assertAvailable(): void {
    if (!this.hostAvailable) {
      throw new NativeMessagingError("host_unavailable");
    }
  }
}

interface Harness {
  area: MemoryStorageArea;
  storage: ExtensionStorage;
  client: FakeNativeMessagingClient;
  runtime: FakeRuntime;
  alarms: FakeAlarms;
  coordinator: BackgroundCoordinator;
  advance(ms: number): void;
}

function makeHarness(area: MemoryStorageArea = new MemoryStorageArea()): Harness {
  const storage = new ExtensionStorage(area);
  const client = new FakeNativeMessagingClient();
  const runtime = new FakeRuntime();
  const alarms = new FakeAlarms();
  let now = NOW_MS;
  const coordinator = new BackgroundCoordinator({
    client: client as unknown as NativeMessagingClient,
    storage,
    runtime,
    alarms,
    extensionVersion: "0.1.0",
    now: () => now,
  });
  return {
    area,
    storage,
    client,
    runtime,
    alarms,
    coordinator,
    advance: (ms: number) => {
      now += ms;
    },
  };
}

async function flush(): Promise<void> {
  await new Promise<void>((resolve) => setTimeout(resolve, 0));
}

function dispatch(
  runtime: FakeRuntime,
  message: unknown,
  sender: FakeSender = {},
): Promise<BackgroundResponse> {
  return new Promise((resolve) => {
    runtime.onMessage.emit(message, sender, (response) =>
      resolve(response as BackgroundResponse),
    );
  });
}

describe("BackgroundCoordinator message validation", () => {
  it("rejects malformed messages without touching the client or storage", async () => {
    const { coordinator, client, storage, area } = makeHarness();
    const invalidMessages: unknown[] = [
      null,
      "capture_job",
      { type: "capture_job" },
      { type: "capture_job", job: { schemaVersion: 1 } },
      { type: "capture_job", job: jobFor(1), extra: true },
      { type: "popup_snapshot", extra: true },
      { type: "popup_status", beforeJobId: 0 },
      { type: "popup_retry", jobId: 0 },
      { type: "popup_discard", jobId: -1 },
      { type: "unknown" },
    ];

    for (const message of invalidMessages) {
      expect(await coordinator.handleMessage(message, CONTENT_SENDER)).toEqual({
        ok: false,
        errorCategory: "invalid_message",
        message: "Uploader rejected an invalid message",
      });
    }

    expect(client.ensureConnectedCalls).toBe(0);
    expect(client.connectCalls).toEqual([]);
    expect(client.submitCalls).toEqual([]);
    expect(client.statusCalls).toEqual([]);
    expect(area.operations).toEqual([]);
    expect((await storage.snapshot()).pendingHandoffs).toBe(0);
  });

  it("rejects senders that are not this extension", async () => {
    const { coordinator, client } = makeHarness();

    expect(
      await coordinator.handleMessage(
        { type: "popup_snapshot" },
        { id: "other-extension" },
      ),
    ).toEqual({
      ok: false,
      errorCategory: "invalid_message",
      message: "Uploader rejected an invalid message",
    });
    expect(
      await coordinator.handleMessage(
        { type: "capture_job", job: jobFor(1) },
        { id: "other-extension", url: LECCAP_URL },
      ),
    ).toEqual({
      ok: false,
      errorCategory: "invalid_message",
      message: "Uploader rejected an invalid message",
    });
    expect(client.ensureConnectedCalls).toBe(0);
  });

  it("rejects capture_job senders that are not a Leccap page", async () => {
    const { coordinator, client, area } = makeHarness();
    const job = jobFor(1);
    const senders: FakeSender[] = [
      { id: EXTENSION_ID },
      { id: EXTENSION_ID, url: "https://example.com/lecture" },
      { id: EXTENSION_ID, tab: { id: 4, url: "https://example.com/lecture" } },
      {
        id: EXTENSION_ID,
        url: "http://leccap.engin.umich.edu/leccap/player/r/lecture-1",
      },
      {
        id: EXTENSION_ID,
        url: "https://leccap.engin.umich.edu.evil.example/lecture",
      },
    ];

    for (const sender of senders) {
      expect(
        await coordinator.handleMessage({ type: "capture_job", job }, sender),
      ).toEqual({
        ok: false,
        errorCategory: "invalid_message",
        message: "Uploader rejected an invalid message",
      });
    }

    expect(client.submitCalls).toEqual([]);
    expect(area.operations).toEqual([]);
  });

  it("accepts a Leccap tab url when the sender has no top-level url", async () => {
    const { coordinator, client } = makeHarness();
    const job = jobFor(1);
    client.submitResponses = [ackFor(job, "queued")];

    const response = await coordinator.handleMessage(
      { type: "capture_job", job },
      { id: EXTENSION_ID, tab: { id: 4, url: LECCAP_URL } },
    );

    expect(response.ok).toBe(true);
    expect(client.submitCalls).toEqual([job]);
  });
});

describe("BackgroundCoordinator capture handoff", () => {
  it("writes the job to the outbox before submitting and clears it on a queued ack", async () => {
    const { coordinator, client, storage } = makeHarness();
    const job = jobFor(1);
    client.submitResponses = [ackFor(job, "queued")];
    let pendingDuringSubmit = -1;
    client.onSubmit = async () => {
      pendingDuringSubmit = (await storage.pendingHandoffs()).length;
    };

    const response = await coordinator.handleMessage(
      { type: "capture_job", job },
      CONTENT_SENDER,
    );

    expect(pendingDuringSubmit).toBe(1);
    expect(client.submitCalls).toEqual([job]);
    expect(response.ok).toBe(true);
    expect(response.status).toBe("queued");
    expect(response.message).toBe("Queued for upload");
    expect(response.snapshot?.pendingHandoffs).toBe(0);
    expect((await storage.snapshot()).lastOutcome).toMatchObject({
      status: "queued",
      lectureKey: job.lectureKey,
      contentHash: job.contentHash,
      action: "none",
    });
  });

  it("keeps the outbox entry and reports waiting_for_uploader when the host is unavailable", async () => {
    const { coordinator, client, storage } = makeHarness();
    const job = jobFor(1);
    client.hostAvailable = false;

    const response = await coordinator.handleMessage(
      { type: "capture_job", job },
      CONTENT_SENDER,
    );

    expect(response.ok).toBe(false);
    expect(response.status).toBe("waiting_for_uploader");
    expect(response.errorCategory).toBe("host_unavailable");
    expect(response.message).toBe("Waiting for local uploader");
    expect(response.snapshot?.pendingHandoffs).toBe(1);
    expect(client.submitCalls).toEqual([]);
    expect((await storage.snapshot()).lastOutcome).toBeNull();
  });

  it("replays a pending handoff after a worker restart and treats already_queued as definitive", async () => {
    const area = new MemoryStorageArea();
    const first = makeHarness(area);
    const job = jobFor(1);
    first.client.hostAvailable = false;
    await first.coordinator.handleMessage(
      { type: "capture_job", job },
      CONTENT_SENDER,
    );
    expect(await first.storage.pendingHandoffs()).toHaveLength(1);

    const restarted = makeHarness(area);
    restarted.client.submitResponses = [ackFor(job, "already_queued")];

    await restarted.coordinator.handleAlarm({ name: DRAIN_ALARM_NAME });

    expect(restarted.client.submitCalls).toEqual([job]);
    expect(await restarted.storage.pendingHandoffs()).toHaveLength(0);
    expect((await restarted.storage.snapshot()).lastOutcome).toMatchObject({
      status: "already_queued",
      lectureKey: job.lectureKey,
      contentHash: job.contentHash,
      action: "none",
    });

    await restarted.coordinator.handleAlarm({ name: DRAIN_ALARM_NAME });
    expect(restarted.client.submitCalls).toHaveLength(1);
  });

  it("replays several pending handoffs with mixed definitive acks", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    const first = jobFor(1);
    const second = jobFor(2);
    await storage.addPendingHandoff(first);
    await storage.addPendingHandoff(second);
    const { coordinator, client } = makeHarness(area);
    client.submitResponses = [
      ackFor(first, "queued"),
      ackFor(second, "already_queued"),
    ];

    await coordinator.handleAlarm({ name: DRAIN_ALARM_NAME });

    expect(client.submitCalls).toEqual([first, second]);
    expect((await storage.snapshot()).pendingHandoffs).toBe(0);
  });

  it("keeps the full copy when a submit ack echoes a different identity", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    const job = jobFor(1);
    await storage.addPendingHandoff(job);
    const { coordinator, client } = makeHarness(area);
    client.submitResponses = [
      ackFor(job, "queued", { contentHash: "0".repeat(64) }),
    ];

    await coordinator.handleAlarm({ name: DRAIN_ALARM_NAME });

    const snapshot = await storage.snapshot();
    expect(snapshot.pendingHandoffs).toBe(1);
    expect(snapshot.overflowNotices).toEqual([]);
    expect(snapshot.lastOutcome).toMatchObject({
      status: "protocol_mismatch",
      lectureKey: job.lectureKey,
      contentHash: job.contentHash,
      action: "none",
    });
  });

  it("records a metadata-only overflow notice for a submit rejection and clears the full copy", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    const job = jobFor(1);
    await storage.addPendingHandoff(job);
    const { coordinator, client } = makeHarness(area);
    client.submitResponses = [ackFor(job, "rejected_queue_full")];

    await coordinator.handleAlarm({ name: DRAIN_ALARM_NAME });

    const snapshot = await storage.snapshot();
    expect(snapshot.pendingHandoffs).toBe(0);
    expect(snapshot.overflowNotices).toEqual([
      {
        lectureKey: job.lectureKey,
        lectureDate: job.lectureDate,
        capturedAt: job.capturedAt,
        reason: "rejected_queue_full",
      },
    ]);
    expect(Object.keys(snapshot.overflowNotices[0]).sort()).toEqual([
      "capturedAt",
      "lectureDate",
      "lectureKey",
      "reason",
    ]);
    expect(snapshot.lastOutcome).toMatchObject({
      status: "rejected_queue_full",
      lectureKey: job.lectureKey,
      contentHash: job.contentHash,
      action: "none",
    });
  });

  it("records the duplicate-terminal recapture action from the ack", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    const job = jobFor(1);
    await storage.addPendingHandoff(job);
    const { coordinator, client } = makeHarness(area);
    client.submitResponses = [
      ackFor(job, "rejected_duplicate_terminal", {
        existingStatus: "permanent_conflict",
        action: "discard_existing_then_recapture",
      }),
    ];

    await coordinator.handleAlarm({ name: DRAIN_ALARM_NAME });

    const snapshot = await storage.snapshot();
    expect(snapshot.pendingHandoffs).toBe(0);
    expect(snapshot.overflowNotices[0].reason).toBe(
      "rejected_duplicate_terminal",
    );
    expect(snapshot.lastOutcome).toMatchObject({
      status: "rejected_duplicate_terminal",
      action: "discard_existing_then_recapture",
    });
  });

  it("reports rejected_handoff_full when the outbox is full and preserves older jobs", async () => {
    const { coordinator, client, storage } = makeHarness();
    client.hostAvailable = false;
    const jobs = [jobFor(1), jobFor(2), jobFor(3)];
    for (const job of jobs) {
      await coordinator.handleMessage(
        { type: "capture_job", job },
        CONTENT_SENDER,
      );
    }
    const fourth = jobFor(4);

    const response = await coordinator.handleMessage(
      { type: "capture_job", job: fourth },
      CONTENT_SENDER,
    );

    expect(response.ok).toBe(false);
    expect(response.status).toBe("rejected_handoff_full");
    expect(response.errorCategory).toBe("rejected_handoff_full");
    expect(response.message).toBe("Rejected — pending handoff is full");
    expect(await storage.snapshot()).toMatchObject({ pendingHandoffs: 3 });
    expect(
      (await storage.pendingHandoffs()).map((entry) => entry.job.lectureKey),
    ).toEqual(jobs.map((job) => job.lectureKey));
    expect((await storage.snapshot()).overflowNotices).toEqual([
      {
        lectureKey: fourth.lectureKey,
        lectureDate: fourth.lectureDate,
        capturedAt: fourth.capturedAt,
        reason: "rejected_handoff_full",
      },
    ]);
    expect((await storage.snapshot()).lastOutcome).toMatchObject({
      status: "rejected_handoff_full",
      action: "none",
    });
  });

  it("appends the recapture instruction when the overflow notice list is also full", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    for (let index = 1; index <= MAX_OVERFLOW_NOTICES; index += 1) {
      await storage.addOverflowNotice({
        lectureKey: `eecs484/2026-fall/${String(index).padStart(3, "0")}`,
        lectureDate: "2026-09-01",
        capturedAt: CAPTURED_AT,
        reason: "rejected_queue_full",
      });
    }
    await storage.addPendingHandoff(jobFor(1));
    await storage.addPendingHandoff(jobFor(2));
    await storage.addPendingHandoff(jobFor(3));
    const { coordinator } = makeHarness(area);

    const response = await coordinator.handleMessage(
      { type: "capture_job", job: jobFor(4) },
      CONTENT_SENDER,
    );

    expect(response.status).toBe("rejected_handoff_full");
    expect(response.message).toBe(
      "Rejected — pending handoff is full; reopen the transcript",
    );
  });
});

describe("BackgroundCoordinator drain lifecycle", () => {
  const openStates: DrainState[] = ["working", "authorizing"];
  const closeStates: DrainState[] = ["idle", "waiting_for_backoff"];

  it.each(openStates)(
    "keeps the alarm port open while the host reports %s",
    async (drainState) => {
      const { coordinator, client } = makeHarness();
      client.connectStatus = status({ drainState });
      client.statusPage = status({ drainState });

      await coordinator.handleAlarm({ name: DRAIN_ALARM_NAME });

      expect(client.ensureConnectedCalls).toBe(1);
      expect(client.connectCalls).toEqual(["0.1.0"]);
      expect(client.statusCalls).toEqual([
        { beforeJobId: undefined, limit: MAX_STATUS_PAGE },
      ]);
      expect(client.disconnectCount).toBe(0);
    },
  );

  it.each(closeStates)(
    "closes the alarm port after the host reports %s",
    async (drainState) => {
      const { coordinator, client } = makeHarness();
      client.connectStatus = status({ drainState });
      client.statusPage = status({ drainState });

      await coordinator.handleAlarm({ name: DRAIN_ALARM_NAME });

      expect(client.disconnectCount).toBe(1);
    },
  );

  it("closes the alarm port only after a later status reports idle", async () => {
    const { coordinator, client } = makeHarness();
    client.connectStatus = status({ drainState: "working" });
    client.statusPage = status({ drainState: "working" });

    await coordinator.handleAlarm({ name: DRAIN_ALARM_NAME });
    expect(client.disconnectCount).toBe(0);

    client.emitStatus(status({ drainState: "idle" }));
    await flush();
    expect(client.disconnectCount).toBe(1);
  });

  it("keeps the alarm port open for the popup interactive lease", async () => {
    const { coordinator, client, advance } = makeHarness();
    client.connectStatus = status({ drainState: "idle" });
    client.statusPage = status({ drainState: "idle" });

    await coordinator.handleMessage({ type: "popup_snapshot" }, POPUP_SENDER);
    await coordinator.handleAlarm({ name: DRAIN_ALARM_NAME });
    expect(client.disconnectCount).toBe(0);

    advance(30_001);
    client.emitStatus(status({ drainState: "idle" }));
    await flush();
    expect(client.disconnectCount).toBe(1);
  });

  it("treats a host port close as an interruption and replays on the next alarm", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    const job = jobFor(1);
    await storage.addPendingHandoff(job);
    const { coordinator, client } = makeHarness(area);

    client.emitDisconnect();
    expect(await storage.pendingHandoffs()).toHaveLength(1);

    client.submitResponses = [ackFor(job, "queued")];
    await coordinator.handleAlarm({ name: DRAIN_ALARM_NAME });

    expect(client.submitCalls).toEqual([job]);
    expect(await storage.pendingHandoffs()).toHaveLength(0);
  });

  it("ignores alarms other than the drain watchdog", async () => {
    const { coordinator, client } = makeHarness();

    await coordinator.handleAlarm({ name: "some-other-alarm" });

    expect(client.ensureConnectedCalls).toBe(0);
  });
});

describe("BackgroundCoordinator popup commands", () => {
  it("answers popup_snapshot from storage without opening the client", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    const job = jobFor(1);
    await storage.addPendingHandoff(job);
    await storage.addOverflowNotice({
      lectureKey: job.lectureKey,
      lectureDate: job.lectureDate,
      capturedAt: job.capturedAt,
      reason: "rejected_queue_full",
    });
    await storage.setUploaderStatus(status({ drainState: "waiting_for_backoff" }));
    const { coordinator, client } = makeHarness(area);

    const response = await coordinator.handleMessage(
      { type: "popup_snapshot" },
      POPUP_SENDER,
    );

    expect(response.ok).toBe(true);
    expect(response.snapshot).toMatchObject({
      pendingHandoffs: 1,
      uploader: { drainState: "waiting_for_backoff" },
    });
    expect(response.snapshot?.overflowNotices).toHaveLength(1);
    expect(client.ensureConnectedCalls).toBe(0);
  });

  it("drains and replays on popup_connect", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    const job = jobFor(1);
    await storage.addPendingHandoff(job);
    const { coordinator, client } = makeHarness(area);
    client.submitResponses = [ackFor(job, "queued")];

    const response = await coordinator.handleMessage(
      { type: "popup_connect" },
      POPUP_SENDER,
    );

    expect(response.ok).toBe(true);
    expect(response.snapshot?.pendingHandoffs).toBe(0);
    expect(client.connectCalls).toEqual(["0.1.0"]);
  });

  it("keeps the outbox when popup_connect cannot reach the host", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    await storage.addPendingHandoff(jobFor(1));
    const { coordinator, client } = makeHarness(area);
    client.hostAvailable = false;

    const response = await coordinator.handleMessage(
      { type: "popup_connect" },
      POPUP_SENDER,
    );

    expect(response.snapshot?.pendingHandoffs).toBe(1);
    expect(client.connectCalls).toEqual([]);
    expect(await storage.pendingHandoffs()).toHaveLength(1);
  });

  it("persists the first status page and returns older pages without persisting them", async () => {
    const { coordinator, client, storage } = makeHarness();
    client.statusPage = status({ drainState: "waiting_for_backoff" });

    const first = await coordinator.handleMessage(
      { type: "popup_status" },
      POPUP_SENDER,
    );
    expect(first.ok).toBe(true);
    expect(first.snapshot?.uploader?.drainState).toBe("waiting_for_backoff");
    expect((await storage.snapshot()).uploader?.drainState).toBe(
      "waiting_for_backoff",
    );
    expect(client.statusCalls).toEqual([
      { beforeJobId: undefined, limit: MAX_STATUS_PAGE },
    ]);

    client.statusPage = status({ drainState: "working" });
    const older = await coordinator.handleMessage(
      { type: "popup_status", beforeJobId: 7 },
      POPUP_SENDER,
    );

    expect(older.snapshot?.uploader?.drainState).toBe("working");
    expect((await storage.snapshot()).uploader?.drainState).toBe(
      "waiting_for_backoff",
    );
    expect(client.statusCalls[1]).toEqual({
      beforeJobId: 7,
      limit: MAX_STATUS_PAGE,
    });
  });

  it("clears the status and records a reset outcome on an accepted reset", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    await storage.setUploaderStatus(status());
    await storage.addPendingHandoff(jobFor(1));
    const { coordinator, client } = makeHarness(area);
    client.resetResponse = commandResult({
      operation: "reset",
      result: "accepted",
      status: "reset",
    });

    const response = await coordinator.handleMessage(
      { type: "popup_reset" },
      POPUP_SENDER,
    );

    expect(client.resetCalls).toBe(1);
    expect(response.ok).toBe(true);
    expect(response.status).toBe("reset");
    const snapshot = await storage.snapshot();
    expect(snapshot.uploader).toBeNull();
    expect(snapshot.pendingHandoffs).toBe(1);
    expect(snapshot.lastOutcome).toMatchObject({
      status: "reset",
      lectureKey: null,
      contentHash: null,
      message: "GitHub connection reset; queued jobs were kept",
      action: "none",
    });
  });

  it("reports a rejected reset without clearing the status", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    await storage.setUploaderStatus(status());
    const { coordinator, client } = makeHarness(area);
    client.resetResponse = commandResult({
      operation: "reset",
      result: "rejected",
      status: null,
      errorCategory: "ineligible_command",
    });

    const response = await coordinator.handleMessage(
      { type: "popup_reset" },
      POPUP_SENDER,
    );

    expect(response.ok).toBe(false);
    expect((await storage.snapshot()).uploader).not.toBeNull();
    expect(client.resetCalls).toBe(1);
  });

  it("relays an accepted retry and refreshes the status page", async () => {
    const { coordinator, client } = makeHarness();
    client.retryResponse = commandResult({
      operation: "retry",
      result: "accepted",
      status: "queued",
      jobId: 42,
    });

    const response = await coordinator.handleMessage(
      { type: "popup_retry", jobId: 42 },
      POPUP_SENDER,
    );

    expect(client.retryCalls).toEqual([42]);
    expect(response.ok).toBe(true);
    expect(response.status).toBe("queued");
    expect(client.statusCalls).toHaveLength(1);
  });

  it("reports an ineligible retry with the existing job status", async () => {
    const { coordinator, client } = makeHarness();
    client.retryResponse = commandResult({
      operation: "retry",
      result: "rejected",
      status: "permanent_conflict",
      errorCategory: "ineligible_command",
      jobId: 42,
    });

    const response = await coordinator.handleMessage(
      { type: "popup_retry", jobId: 42 },
      POPUP_SENDER,
    );

    expect(response.ok).toBe(false);
    expect(response.status).toBe("permanent_conflict");
    expect(response.message).toBe("Conflict — manual review needed");
    expect(client.statusCalls).toHaveLength(0);
  });

  it("relays an accepted discard", async () => {
    const { coordinator, client } = makeHarness();
    client.discardResponse = commandResult({
      operation: "discard",
      result: "accepted",
      status: "discarded",
      jobId: 9,
    });

    const response = await coordinator.handleMessage(
      { type: "popup_discard", jobId: 9 },
      POPUP_SENDER,
    );

    expect(client.discardCalls).toEqual([9]);
    expect(response.ok).toBe(true);
    expect(response.status).toBe("discarded");
  });

  it("reports a rejected discard with its protocol status", async () => {
    const { coordinator, client } = makeHarness();
    client.discardResponse = commandResult({
      operation: "discard",
      result: "rejected",
      status: "rejected_permission",
      errorCategory: "ineligible_command",
      jobId: 9,
    });

    const response = await coordinator.handleMessage(
      { type: "popup_discard", jobId: 9 },
      POPUP_SENDER,
    );

    expect(response.ok).toBe(false);
    expect(response.status).toBe("rejected_permission");
    expect(response.message).toBe("Rejected — GitHub permission denied");
  });
});

describe("BackgroundCoordinator startup wiring", () => {
  it("creates the one-minute drain alarm and installs listeners exactly once", async () => {
    const { coordinator, runtime, alarms, client } = makeHarness();

    await coordinator.start();
    await coordinator.start();

    expect(DRAIN_ALARM_NAME).toBe("lecture-transcripts-drain");
    expect(DRAIN_PERIOD_MINUTES).toBe(1);
    expect(alarms.createCalls).toEqual([
      { name: DRAIN_ALARM_NAME, info: { periodInMinutes: 1 } },
    ]);
    expect(runtime.onMessage.size).toBe(1);
    expect(runtime.onInstalled.size).toBe(1);
    expect(runtime.onStartup.size).toBe(1);
    expect(alarms.onAlarm.size).toBe(1);
    expect(client.ensureConnectedCalls).toBe(0);
  });

  it("answers runtime.onMessage through the installed listener", async () => {
    const { coordinator, client, runtime } = makeHarness();
    await coordinator.start();
    const job = jobFor(1);
    client.submitResponses = [ackFor(job, "queued")];

    const response = await dispatch(
      runtime,
      { type: "capture_job", job },
      CONTENT_SENDER,
    );

    expect(response.ok).toBe(true);
    expect(client.submitCalls).toEqual([job]);
  });

  it("replays pending handoffs on startup and recreates the alarm on install", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    const job = jobFor(1);
    await storage.addPendingHandoff(job);
    const { coordinator, client, runtime, alarms } = makeHarness(area);
    client.submitResponses = [ackFor(job, "queued")];
    await coordinator.start();

    runtime.onStartup.emit();
    await flush();
    expect(client.submitCalls).toEqual([job]);
    expect(await storage.pendingHandoffs()).toHaveLength(0);
    expect(alarms.createCalls).toHaveLength(2);

    runtime.onInstalled.emit();
    await flush();
    expect(alarms.createCalls).toHaveLength(3);
  });

  it("drains through the installed alarm listener", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    const job = jobFor(1);
    await storage.addPendingHandoff(job);
    const { coordinator, client, alarms } = makeHarness(area);
    client.submitResponses = [ackFor(job, "queued")];
    await coordinator.start();

    alarms.onAlarm.emit({ name: DRAIN_ALARM_NAME });
    await flush();

    expect(client.submitCalls).toEqual([job]);
    expect(await storage.pendingHandoffs()).toHaveLength(0);
  });
});
