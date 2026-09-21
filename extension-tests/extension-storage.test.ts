import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  ExtensionStorage,
  MAX_OVERFLOW_NOTICES,
  MAX_PENDING_HANDOFFS,
  OutboxFullError,
  OverflowNoticeFullError,
  STORAGE_KEY,
  type StorageAreaLike,
} from "../extension/src/extension-storage";
import type { NativeTranscriptJob } from "../extension/src/native-messaging";
import {
  EMPTY_COUNTS,
  type OverflowNotice,
  type UploaderStatus,
} from "../extension/src/status";
import { createTranscriptJob } from "../extension/src/transcript-job";

const NOW = "2026-09-20T12:00:00.000Z";
const LATER = "2026-09-20T12:00:05.000Z";
const CAPTURED_AT = "2026-09-20T11:00:00Z";
const TRANSCRIPT_A =
  "A sufficiently long stable transcript body for the storage contract test.";
const TRANSCRIPT_B =
  "A different sufficiently long transcript body for the deduplication contract test.";

function jobFor(
  lectureNumber: number,
  transcript = TRANSCRIPT_A,
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

function noticeFor(
  lectureNumber: number,
  reason: OverflowNotice["reason"] = "rejected_queue_full",
): OverflowNotice {
  return {
    lectureKey: `eecs484/2026-fall/${String(lectureNumber).padStart(3, "0")}`,
    lectureDate: "2026-09-01",
    capturedAt: CAPTURED_AT,
    reason,
  };
}

function uploaderStatus(
  overrides: Partial<UploaderStatus> = {},
): UploaderStatus {
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

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(NOW));
});

afterEach(() => {
  vi.useRealTimers();
});

describe("ExtensionStorage pending-handoff outbox", () => {
  it("matches the contract's bounded capacities", () => {
    expect(MAX_PENDING_HANDOFFS).toBe(3);
    expect(MAX_OVERFLOW_NOTICES).toBe(20);
    expect(STORAGE_KEY).toBe("lectureTranscriptsExtensionState");
  });

  it("bounds the outbox at three full jobs and rejects a fourth without evicting", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    const jobs = [jobFor(1), jobFor(2), jobFor(3)];
    for (const job of jobs) {
      await storage.addPendingHandoff(job);
    }

    await expect(storage.addPendingHandoff(jobFor(4))).rejects.toBeInstanceOf(
      OutboxFullError,
    );

    const pending = await storage.pendingHandoffs();
    expect(pending.map((entry) => entry.job.lectureKey)).toEqual(
      jobs.map((job) => job.lectureKey),
    );
    expect((await storage.snapshot()).pendingHandoffs).toBe(3);
    expect(area.operations.filter((operation) => operation === "set")).toHaveLength(3);
  });

  it("stores the exact pending-handoff shape as a defensive copy", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);
    const job = jobFor(1);

    const entry = await storage.addPendingHandoff(job);

    expect(Object.keys(entry).sort()).toEqual([
      "capturedAt",
      "contentHash",
      "job",
      "lectureDate",
      "lectureKey",
    ]);
    expect(entry).toEqual({
      lectureKey: job.lectureKey,
      contentHash: job.contentHash,
      lectureDate: job.lectureDate,
      capturedAt: job.capturedAt,
      job,
    });
    expect(entry.job).not.toBe(job);
    expect((await storage.pendingHandoffs())[0].job).not.toBe(job);
  });

  it("deduplicates by lectureKey and contentHash", async () => {
    const storage = new ExtensionStorage(new MemoryStorageArea());
    const job = jobFor(1);

    const first = await storage.addPendingHandoff(job);
    const second = await storage.addPendingHandoff(job);

    expect(second).toEqual(first);
    expect(await storage.pendingHandoffs()).toHaveLength(1);

    const changed = jobFor(1, TRANSCRIPT_B);
    expect(changed.lectureKey).toBe(job.lectureKey);
    expect(changed.contentHash).not.toBe(job.contentHash);
    await storage.addPendingHandoff(changed);
    expect(await storage.pendingHandoffs()).toHaveLength(2);
  });

  it("serializes concurrent read-modify-write operations", async () => {
    const area = new MemoryStorageArea();
    const storage = new ExtensionStorage(area);

    await Promise.all([
      storage.addPendingHandoff(jobFor(1)),
      storage.addPendingHandoff(jobFor(2)),
    ]);

    expect(area.operations).toEqual(["get", "set", "get", "set"]);
    expect(
      (await storage.pendingHandoffs()).map((entry) => entry.job.lectureKey),
    ).toEqual([jobFor(1).lectureKey, jobFor(2).lectureKey]);
  });

  it("removes an acknowledged handoff exactly once", async () => {
    const storage = new ExtensionStorage(new MemoryStorageArea());
    const job = jobFor(1);
    await storage.addPendingHandoff(job);

    expect(
      await storage.removePendingHandoff(job.lectureKey, "0".repeat(64)),
    ).toBe(false);
    expect(
      await storage.removePendingHandoff(job.lectureKey, job.contentHash),
    ).toBe(true);
    expect(
      await storage.removePendingHandoff(job.lectureKey, job.contentHash),
    ).toBe(false);
    expect((await storage.snapshot()).pendingHandoffs).toBe(0);
  });

  it("replays the outbox from a new store over the same backing data", async () => {
    const area = new MemoryStorageArea();
    const original = new ExtensionStorage(area);
    const job = jobFor(2);
    await original.addPendingHandoff(job);
    await original.addOverflowNotice(noticeFor(2));
    await original.setUploaderStatus(
      uploaderStatus({ drainState: "waiting_for_backoff" }),
    );
    await original.recordOutcome({
      status: "queued",
      lectureKey: job.lectureKey,
      contentHash: job.contentHash,
      message: "Queued for upload",
      action: "none",
      at: CAPTURED_AT,
    });

    const restarted = new ExtensionStorage(area);
    const snapshot = await restarted.snapshot();

    expect(snapshot.pendingHandoffs).toBe(1);
    expect(snapshot.overflowNotices).toEqual([noticeFor(2)]);
    expect(snapshot.uploader?.drainState).toBe("waiting_for_backoff");
    expect(snapshot.lastOutcome).toEqual({
      status: "queued",
      lectureKey: job.lectureKey,
      contentHash: job.contentHash,
      message: "Queued for upload",
      action: "none",
      at: CAPTURED_AT,
    });
    expect(await restarted.pendingHandoffs()).toEqual([
      {
        lectureKey: job.lectureKey,
        contentHash: job.contentHash,
        lectureDate: job.lectureDate,
        capturedAt: job.capturedAt,
        job,
      },
    ]);
  });
});

describe("ExtensionStorage overflow notices", () => {
  it("stores metadata-only notices newest-first and suppresses duplicates", async () => {
    const storage = new ExtensionStorage(new MemoryStorageArea());
    const older = noticeFor(1, "rejected_handoff_full");
    const newer = noticeFor(2, "rejected_queue_full");

    await storage.addOverflowNotice(older);
    await storage.addOverflowNotice(newer);
    await storage.addOverflowNotice(older);
    await storage.addOverflowNotice(noticeFor(1, "rejected_queue_full"));

    const notices = (await storage.snapshot()).overflowNotices;
    expect(notices).toHaveLength(3);
    expect(notices[0]).toEqual(noticeFor(1, "rejected_queue_full"));
    expect(notices[1]).toEqual(newer);
    expect(notices[2]).toEqual(older);
    for (const notice of notices) {
      expect(Object.keys(notice).sort()).toEqual([
        "capturedAt",
        "lectureDate",
        "lectureKey",
        "reason",
      ]);
    }
  });

  it("bounds notices at twenty and never evicts an older notice", async () => {
    const storage = new ExtensionStorage(new MemoryStorageArea());
    for (let index = 1; index <= MAX_OVERFLOW_NOTICES; index += 1) {
      await storage.addOverflowNotice(noticeFor(index));
    }

    await expect(
      storage.addOverflowNotice(noticeFor(MAX_OVERFLOW_NOTICES + 1)),
    ).rejects.toBeInstanceOf(OverflowNoticeFullError);

    const notices = (await storage.snapshot()).overflowNotices;
    expect(notices).toHaveLength(MAX_OVERFLOW_NOTICES);
    expect(notices.at(-1)).toEqual(noticeFor(1));
  });

  it("removes a notice by valid index only", async () => {
    const storage = new ExtensionStorage(new MemoryStorageArea());
    await storage.addOverflowNotice(noticeFor(1));
    await storage.addOverflowNotice(noticeFor(2));

    expect(await storage.removeOverflowNotice(-1)).toBe(false);
    expect(await storage.removeOverflowNotice(2)).toBe(false);
    expect(await storage.removeOverflowNotice(1.5)).toBe(false);
    expect(await storage.removeOverflowNotice(0)).toBe(true);
    expect((await storage.snapshot()).overflowNotices).toEqual([noticeFor(1)]);
  });
});

describe("ExtensionStorage status and snapshot", () => {
  it("exposes the exact snapshot shape for an empty store", async () => {
    const storage = new ExtensionStorage(new MemoryStorageArea());

    const snapshot = await storage.snapshot();

    expect(Object.keys(snapshot).sort()).toEqual([
      "lastOutcome",
      "overflowNotices",
      "pendingHandoffs",
      "updatedAt",
      "uploader",
    ]);
    expect(snapshot).toEqual({
      uploader: null,
      pendingHandoffs: 0,
      overflowNotices: [],
      lastOutcome: null,
      updatedAt: NOW,
    });
  });

  it("persists uploader status, bounds outcomes, and clears only the status", async () => {
    const storage = new ExtensionStorage(new MemoryStorageArea());
    const job = jobFor(1);
    const status = uploaderStatus({ drainState: "working" });

    await storage.setUploaderStatus(status);
    status.drainState = "idle";
    expect((await storage.snapshot()).uploader?.drainState).toBe("working");

    const snapshot = await storage.snapshot();
    snapshot.uploader!.drainState = "authorizing";
    expect((await storage.snapshot()).uploader?.drainState).toBe("working");

    await storage.recordOutcome({
      status: "rejected_queue_full",
      lectureKey: job.lectureKey,
      contentHash: job.contentHash,
      message: "m".repeat(300),
      action: "none",
    });
    const outcome = (await storage.snapshot()).lastOutcome;
    expect(outcome?.status).toBe("rejected_queue_full");
    expect(outcome?.message).toHaveLength(256);
    expect(outcome?.at).toBe(NOW);

    await storage.clearStatus();
    const cleared = await storage.snapshot();
    expect(cleared.uploader).toBeNull();
    expect(cleared.lastOutcome?.status).toBe("rejected_queue_full");
  });

  it("stamps updatedAt from the storage clock on every write", async () => {
    const storage = new ExtensionStorage(new MemoryStorageArea());
    await storage.addPendingHandoff(jobFor(1));
    expect((await storage.snapshot()).updatedAt).toBe(NOW);

    vi.setSystemTime(new Date(LATER));
    await storage.setUploaderStatus(uploaderStatus());
    expect((await storage.snapshot()).updatedAt).toBe(LATER);
  });

  it("clamps persisted state to the contract bounds on read", async () => {
    const area = new MemoryStorageArea();
    area.seed({
      [STORAGE_KEY]: {
        pendingHandoffs: Array.from({ length: 5 }, (_, index) => ({
          lectureKey: `key-${index}`,
        })),
        overflowNotices: Array.from({ length: 25 }, (_, index) =>
          noticeFor(index + 1),
        ),
        lastStatus: null,
        lastOutcome: null,
        updatedAt: 42,
      },
    });
    const storage = new ExtensionStorage(area);

    const snapshot = await storage.snapshot();

    expect(snapshot.pendingHandoffs).toBe(MAX_PENDING_HANDOFFS);
    expect(snapshot.overflowNotices).toHaveLength(MAX_OVERFLOW_NOTICES);
    expect(snapshot.uploader).toBeNull();
    expect(snapshot.updatedAt).toBe(NOW);
  });

  it("treats a non-object persisted value as empty state", async () => {
    const area = new MemoryStorageArea();
    area.seed({ [STORAGE_KEY]: "garbage" });
    const storage = new ExtensionStorage(area);

    const snapshot = await storage.snapshot();

    expect(snapshot.pendingHandoffs).toBe(0);
    expect(snapshot.overflowNotices).toEqual([]);
    expect(snapshot.uploader).toBeNull();
  });
});
