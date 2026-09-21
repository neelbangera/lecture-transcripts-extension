import {
  MAX_STATUS_PAGE,
  NativeMessagingClient,
  NativeMessagingError,
  NativeProtocolError,
  type AckResponse,
  type NativeTranscriptJob,
  type SubmitAckStatus,
  isNativeTranscriptJob,
} from "./native-messaging";
import {
  ExtensionStorage,
  OutboxFullError,
  OverflowNoticeFullError,
  type PendingHandoff,
} from "./extension-storage";
import {
  errorLabel,
  statusLabel,
  type ExtensionSnapshot,
  type LastOutcome,
  type OverflowNotice,
  type UploaderStatus,
} from "./status";

export const DRAIN_ALARM_NAME = "lecture-transcripts-drain";
export const DRAIN_PERIOD_MINUTES = 1;
const INTERACTIVE_LEASE_MS = 30_000;

type MessageSender = {
  id?: string;
  url?: string;
  tab?: { id?: number; url?: string };
};

type MessageListener = (
  message: unknown,
  sender: MessageSender,
  sendResponse: (response: unknown) => void,
) => boolean | void;

interface EventLike<T extends (...args: any[]) => void> {
  addListener(listener: T): void;
}

interface RuntimeLike {
  id?: string;
  getManifest?: () => { version?: string };
  onMessage?: EventLike<MessageListener>;
  onInstalled?: EventLike<() => void>;
  onStartup?: EventLike<() => void>;
}

interface AlarmLike {
  name: string;
}

interface AlarmsLike {
  create(name: string, info: { periodInMinutes: number }): Promise<void> | void;
  onAlarm?: EventLike<(alarm: AlarmLike) => void>;
}

interface ChromeLike {
  runtime?: RuntimeLike;
  alarms?: AlarmsLike;
}

export type ExtensionMessage =
  | { type: "capture_job"; job: NativeTranscriptJob }
  | { type: "popup_snapshot" }
  | { type: "popup_connect" }
  | { type: "popup_reset" }
  | { type: "popup_status"; beforeJobId?: number }
  | { type: "popup_retry"; jobId: number }
  | { type: "popup_discard"; jobId: number };

export interface BackgroundResponse {
  ok: boolean;
  snapshot?: ExtensionSnapshot;
  errorCategory?: string;
  status?: string;
  message?: string;
}

export interface BackgroundCoordinatorOptions {
  client?: NativeMessagingClient;
  storage?: ExtensionStorage;
  runtime?: RuntimeLike;
  alarms?: AlarmsLike;
  extensionVersion?: string;
  now?: () => number;
}

function chromeApi(): ChromeLike | undefined {
  return (globalThis as { chrome?: ChromeLike }).chrome;
}

function runtimeApi(): RuntimeLike | undefined {
  return chromeApi()?.runtime;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasExactKeys(value: Record<string, unknown>, required: readonly string[], optional: readonly string[] = []): boolean {
  const allowed = new Set([...required, ...optional]);
  const keys = Object.keys(value);
  return required.every((key) => Object.prototype.hasOwnProperty.call(value, key)) && keys.every((key) => allowed.has(key));
}

function isPositiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function isLeccapUrl(value: unknown): boolean {
  if (typeof value !== "string") return false;
  try {
    const url = new URL(value);
    return url.protocol === "https:" && url.hostname === "leccap.engin.umich.edu";
  } catch {
    return false;
  }
}

function isExtensionMessage(value: unknown): value is ExtensionMessage {
  if (!isObject(value) || typeof value.type !== "string") return false;
  switch (value.type) {
    case "capture_job":
      return hasExactKeys(value, ["type", "job"]) && isNativeTranscriptJob(value.job);
    case "popup_snapshot":
    case "popup_connect":
    case "popup_reset":
      return hasExactKeys(value, ["type"]);
    case "popup_status":
      return hasExactKeys(value, ["type"], ["beforeJobId"]) && (value.beforeJobId === undefined || isPositiveInteger(value.beforeJobId));
    case "popup_retry":
    case "popup_discard":
      return hasExactKeys(value, ["type", "jobId"]) && isPositiveInteger(value.jobId);
    default:
      return false;
  }
}

function isFromThisExtension(sender: MessageSender, runtime: RuntimeLike): boolean {
  return !sender.id || !runtime.id || sender.id === runtime.id;
}

function isFromAllowedContentPage(sender: MessageSender): boolean {
  return isLeccapUrl(sender.url ?? sender.tab?.url);
}

function safeErrorCategory(error: unknown): string {
  if (error instanceof NativeMessagingError) return error.category;
  if (error instanceof NativeProtocolError) return "protocol_mismatch";
  return "internal";
}

function responseError(error: unknown): BackgroundResponse {
  const category = safeErrorCategory(error);
  return { ok: false, errorCategory: category, status: category, message: errorLabel(category) };
}

function noticeFor(job: NativeTranscriptJob, reason: OverflowNotice["reason"]): OverflowNotice {
  return {
    lectureKey: job.lectureKey,
    lectureDate: job.lectureDate,
    capturedAt: job.capturedAt,
    reason,
  };
}

function isSubmitRejection(status: SubmitAckStatus): status is Exclude<SubmitAckStatus, "queued" | "already_queued"> {
  return status !== "queued" && status !== "already_queued";
}

export class BackgroundCoordinator {
  private started = false;
  private drainRunning: Promise<void> | null = null;
  private alarmCycleActive = false;
  private interactiveLeaseUntil = 0;
  private lastDrainState: UploaderStatus["drainState"] = "idle";
  private readonly extensionVersion: string;

  constructor(private readonly options: BackgroundCoordinatorOptions = {}) {
    this.client = options.client ?? new NativeMessagingClient();
    this.storage = options.storage ?? new ExtensionStorage();
    this.runtime = options.runtime ?? runtimeApi();
    this.extensionVersion = options.extensionVersion ?? this.readExtensionVersion();
    this.alarms = options.alarms ?? chromeApi()?.alarms;
    this.now = options.now ?? Date.now;
    this.client.onStatus((status) => {
      void this.handleStatus(status, true);
    });
    this.client.onDisconnect(() => {
      // An interrupted port is never a definitive handoff result. The outbox
      // remains intact and the next alarm replays it.
      this.lastDrainState = "idle";
    });
  }

  private readonly client: NativeMessagingClient;
  private readonly storage: ExtensionStorage;
  private readonly runtime: RuntimeLike | undefined;
  private readonly alarms: AlarmsLike | undefined;
  private readonly now: () => number;

  async start(): Promise<void> {
    if (this.started) return;
    this.started = true;
    this.installListeners();
    await this.ensureAlarm();
  }

  async handleMessage(message: unknown, sender: MessageSender = {}): Promise<BackgroundResponse> {
    if (!isExtensionMessage(message)) return { ok: false, errorCategory: "invalid_message", message: errorLabel("invalid_message") };
    const runtime = this.runtime;
    if (!runtime || !isFromThisExtension(sender, runtime)) {
      return { ok: false, errorCategory: "invalid_message", message: errorLabel("invalid_message") };
    }
    if (message.type === "capture_job" && !isFromAllowedContentPage(sender)) {
      return { ok: false, errorCategory: "invalid_message", message: errorLabel("invalid_message") };
    }

    switch (message.type) {
      case "capture_job":
        return this.handleCapture(message.job);
      case "popup_snapshot":
        this.markInteractive();
        return { ok: true, snapshot: await this.storage.snapshot() };
      case "popup_connect":
        this.markInteractive();
        try {
          await this.runDrainCycle();
          return { ok: true, snapshot: await this.storage.snapshot() };
        } catch (error) {
          return { ...responseError(error), snapshot: await this.storage.snapshot() };
        }
      case "popup_reset":
        this.markInteractive();
        return this.handleReset();
      case "popup_status":
        this.markInteractive();
        return this.handleStatusRequest(message.beforeJobId);
      case "popup_retry":
        this.markInteractive();
        return this.handleRetry(message.jobId);
      case "popup_discard":
        this.markInteractive();
        return this.handleDiscard(message.jobId);
    }
  }

  async handleAlarm(alarm: AlarmLike): Promise<void> {
    if (alarm.name !== DRAIN_ALARM_NAME) return;
    await this.runDrainCycle();
  }

  private installListeners(): void {
    this.runtime?.onMessage?.addListener((message, sender, sendResponse) => {
      void this.handleMessage(message, sender).then(sendResponse).catch((error) => sendResponse(responseError(error)));
      return true;
    });
    this.runtime?.onInstalled?.addListener(() => {
      void this.ensureAlarm();
    });
    this.runtime?.onStartup?.addListener(() => {
      void this.ensureAlarm();
      void this.runDrainCycle();
    });
    this.alarms?.onAlarm?.addListener((alarm) => {
      void this.handleAlarm(alarm);
    });
  }

  private async ensureAlarm(): Promise<void> {
    if (!this.alarms) return;
    await this.alarms.create(DRAIN_ALARM_NAME, { periodInMinutes: DRAIN_PERIOD_MINUTES });
  }

  private async handleCapture(job: NativeTranscriptJob): Promise<BackgroundResponse> {
    try {
      await this.storage.addPendingHandoff(job);
    } catch (error) {
      if (!(error instanceof OutboxFullError)) return responseError(error);
      const saved = await this.tryRecordNotice(job, "rejected_handoff_full");
      await this.recordOutcome(job, "rejected_handoff_full", "none");
      return {
        ok: false,
        status: "rejected_handoff_full",
        errorCategory: "rejected_handoff_full",
        message: saved ? statusLabel("rejected_handoff_full") : `${statusLabel("rejected_handoff_full")}; reopen the transcript`,
      };
    }

    try {
      await this.runDrainCycle();
      const snapshot = await this.storage.snapshot();
      const outcome = snapshot.lastOutcome;
      // A submit rejection removes the pending copy once its notice is saved,
      // so an empty outbox alone is not proof of acceptance. Surface the
      // rejection instead of reporting a false queued result.
      if (
        snapshot.pendingHandoffs === 0 &&
        outcome !== null &&
        outcome.lectureKey === job.lectureKey &&
        outcome.contentHash === job.contentHash &&
        outcome.status.startsWith("rejected_")
      ) {
        return {
          ok: false,
          snapshot,
          status: outcome.status,
          errorCategory: outcome.status,
          message: outcome.message,
        };
      }
      const stillPending = snapshot.pendingHandoffs > 0;
      return {
        ok: !stillPending,
        snapshot,
        status: stillPending ? "waiting_for_uploader" : "queued",
        errorCategory: stillPending ? "host_unavailable" : undefined,
        message: stillPending ? statusLabel("waiting_for_uploader") : statusLabel("queued"),
      };
    } catch (error) {
      return { ...responseError(error), snapshot: await this.storage.snapshot(), status: "waiting_for_uploader" };
    }
  }

  private async runDrainCycle(): Promise<void> {
    if (this.drainRunning) return this.drainRunning;
    this.drainRunning = this.drain().finally(() => {
      this.drainRunning = null;
    });
    return this.drainRunning;
  }

  private async drain(): Promise<void> {
    this.alarmCycleActive = true;
    try {
      await this.client.ensureConnected();
      const connected = await this.client.connect(this.extensionVersion);
      await this.handleStatus(connected, true);
      const firstPage = await this.client.statusRequest(undefined, MAX_STATUS_PAGE);
      await this.handleStatus(firstPage, true);
      await this.replayPendingHandoffs();
      // The host owns retry timing. The alarm-owned port can close only after
      // a status explicitly says there is no active/due work.
      if (this.lastDrainState === "idle" || this.lastDrainState === "waiting_for_backoff") {
        this.closeAlarmPortIfIdle();
      }
    } catch (error) {
      if (!(error instanceof NativeMessagingError) || error.category !== "host_unavailable") {
        await this.storage.recordOutcome({
          status: safeErrorCategory(error),
          lectureKey: null,
          contentHash: null,
          message: errorLabel(safeErrorCategory(error)),
          action: "none",
        });
      }
      // Keep the full outbox copy. A later alarm will reconnect and replay it.
    } finally {
      this.alarmCycleActive = false;
      this.closeAlarmPortIfIdle();
    }
  }

  private async replayPendingHandoffs(): Promise<void> {
    const pending = await this.storage.pendingHandoffs();
    for (const entry of pending) {
      try {
        const ack = await this.client.submitJob(entry.job);
        await this.acceptAck(entry, ack);
      } catch (error) {
        if (error instanceof NativeMessagingError && error.category === "host_unavailable") return;
        await this.storage.recordOutcome({
          status: safeErrorCategory(error),
          lectureKey: entry.lectureKey,
          contentHash: entry.contentHash,
          message: errorLabel(safeErrorCategory(error)),
          action: "none",
        });
        return;
      }
    }
  }

  private async acceptAck(entry: PendingHandoff, ack: AckResponse): Promise<void> {
    if (ack.lectureKey !== entry.lectureKey || ack.contentHash !== entry.contentHash) {
      throw new NativeProtocolError("submit acknowledgement did not echo the submitted identity");
    }

    if (ack.status === "queued" || ack.status === "already_queued") {
      await this.storage.removePendingHandoff(entry.lectureKey, entry.contentHash);
      await this.recordOutcome(entry.job, ack.status, "none");
      return;
    }

    if (isSubmitRejection(ack.status)) {
      const reason = ack.status as OverflowNotice["reason"];
      const saved = await this.tryRecordNotice(entry.job, reason);
      if (saved) {
        await this.storage.removePendingHandoff(entry.lectureKey, entry.contentHash);
      }
      await this.recordOutcome(entry.job, ack.status, ack.action ?? "none");
    }
  }

  private async handleStatus(status: UploaderStatus, persist: boolean): Promise<void> {
    this.lastDrainState = status.drainState;
    if (persist) await this.storage.setUploaderStatus(status);
    this.closeAlarmPortIfIdle();
  }

  private async handleStatusRequest(beforeJobId?: number): Promise<BackgroundResponse> {
    try {
      await this.client.ensureConnected();
      const status = await this.client.statusRequest(beforeJobId, MAX_STATUS_PAGE);
      await this.handleStatus(status, beforeJobId === undefined);
      const snapshot = await this.storage.snapshot();
      // Older pages are intentionally not persisted as the worker's
      // last-known live status, but the popup still needs the page it asked
      // for in this response.
      if (beforeJobId !== undefined) snapshot.uploader = status;
      return { ok: true, snapshot };
    } catch (error) {
      return { ...responseError(error), snapshot: await this.storage.snapshot() };
    }
  }

  private async handleReset(): Promise<BackgroundResponse> {
    try {
      await this.client.ensureConnected();
      const result = await this.client.reset();
      if (result.result === "accepted") {
        await this.storage.clearStatus();
        await this.storage.recordOutcome({
          status: "reset",
          lectureKey: null,
          contentHash: null,
          message: "GitHub connection reset; queued jobs were kept",
          action: "none",
        });
      }
      return { ok: result.result === "accepted", status: result.status ?? result.errorCategory ?? "internal", snapshot: await this.storage.snapshot() };
    } catch (error) {
      return { ...responseError(error), snapshot: await this.storage.snapshot() };
    }
  }

  private async handleRetry(jobId: number): Promise<BackgroundResponse> {
    try {
      await this.client.ensureConnected();
      const result = await this.client.retryJob(jobId);
      const status = result.status ?? result.errorCategory ?? "internal";
      if (result.result === "accepted") await this.handleStatusRequest();
      return { ok: result.result === "accepted", status, message: statusLabel(status), snapshot: await this.storage.snapshot() };
    } catch (error) {
      return { ...responseError(error), snapshot: await this.storage.snapshot() };
    }
  }

  private async handleDiscard(jobId: number): Promise<BackgroundResponse> {
    try {
      await this.client.ensureConnected();
      const result = await this.client.discardJob(jobId);
      const status = result.status ?? result.errorCategory ?? "internal";
      if (result.result === "accepted") await this.handleStatusRequest();
      return { ok: result.result === "accepted", status, message: statusLabel(status), snapshot: await this.storage.snapshot() };
    } catch (error) {
      return { ...responseError(error), snapshot: await this.storage.snapshot() };
    }
  }

  private async tryRecordNotice(job: NativeTranscriptJob, reason: OverflowNotice["reason"]): Promise<boolean> {
    try {
      await this.storage.addOverflowNotice(noticeFor(job, reason));
      return true;
    } catch (error) {
      if (error instanceof OverflowNoticeFullError) return false;
      throw error;
    }
  }

  private async recordOutcome(job: NativeTranscriptJob, status: string, action: LastOutcome["action"]): Promise<void> {
    await this.storage.recordOutcome({
      status,
      lectureKey: job.lectureKey,
      contentHash: job.contentHash,
      message: statusLabel(status),
      action,
    });
  }

  private markInteractive(): void {
    this.interactiveLeaseUntil = this.now() + INTERACTIVE_LEASE_MS;
  }

  private closeAlarmPortIfIdle(): void {
    if (this.alarmCycleActive || this.interactiveLeaseUntil > this.now()) return;
    if (this.lastDrainState !== "idle" && this.lastDrainState !== "waiting_for_backoff") return;
    this.client.disconnect();
  }

  private readExtensionVersion(): string {
    const version = this.runtime?.getManifest?.().version;
    return typeof version === "string" && version.length > 0 ? version : "0.1.0";
  }
}

const runtime = runtimeApi();
if (runtime?.onMessage && chromeApi()?.alarms) {
  const coordinator = new BackgroundCoordinator();
  void coordinator.start();
}
