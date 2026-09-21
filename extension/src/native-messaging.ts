import {
  AUTH_STATES,
  DRAIN_STATES,
  EMPTY_COUNTS,
  ERROR_CATEGORIES,
  QUEUE_STATUSES,
  type AuthState,
  type CountMap,
  type DrainState,
  type ErrorCategory,
  type JobSummary,
  type QueueStatus,
  type RemoteFileKind,
  type UploaderStatus,
} from "./status";
import { validateTranscriptJob, type TranscriptJob } from "./transcript-job";

export const PROTOCOL_VERSION = 1 as const;
export const NATIVE_HOST_NAME = "com.neelbangera.lecturetranscripts";
export const MAX_NATIVE_MESSAGE_BYTES = 1_048_576;
export const MAX_STATUS_PAGE = 50;

export type NativeTranscriptJob = TranscriptJob;

export interface ConnectRequest {
  type: "connect";
  protocolVersion: 1;
  requestId: string;
  extensionVersion: string;
}

export interface SubmitJobRequest {
  type: "submit_job";
  protocolVersion: 1;
  requestId: string;
  job: NativeTranscriptJob;
}

export interface StatusRequest {
  type: "status_request";
  protocolVersion: 1;
  requestId: string;
  beforeJobId?: number;
  limit?: number;
}

export interface RetryJobRequest {
  type: "retry_job";
  protocolVersion: 1;
  requestId: string;
  jobId: number;
}

export interface DiscardJobRequest {
  type: "discard_job";
  protocolVersion: 1;
  requestId: string;
  jobId: number;
  confirmation: "discard";
}

export interface ResetRequest {
  type: "reset";
  protocolVersion: 1;
  requestId: string;
}

export type NativeRequest =
  | ConnectRequest
  | SubmitJobRequest
  | StatusRequest
  | RetryJobRequest
  | DiscardJobRequest
  | ResetRequest;

export const SUBMIT_ACK_STATUSES = [
  "queued",
  "already_queued",
  "rejected_invalid_schema",
  "rejected_unknown_field",
  "rejected_oversized",
  "rejected_invalid_hash",
  "rejected_unsafe_url",
  "rejected_queue_full",
  "rejected_duplicate_terminal",
] as const;

export type SubmitAckStatus = (typeof SUBMIT_ACK_STATUSES)[number];

export interface AckResponse {
  type: "ack";
  protocolVersion: 1;
  requestId: string;
  operation: "submit";
  jobId: number | null;
  lectureKey: string | null;
  contentHash: string | null;
  status: SubmitAckStatus;
  existingStatus: QueueStatus | null;
  action: "retry_existing" | "discard_existing_then_recapture" | null;
}

export interface CommandResultResponse {
  type: "command_result";
  protocolVersion: 1;
  requestId: string;
  operation: "retry" | "discard" | "reset";
  jobId: number | null;
  result: "accepted" | "rejected";
  status: QueueStatus | "discarded" | "reset" | null;
  errorCategory: ErrorCategory | null;
}

export interface ErrorResponse {
  type: "error";
  protocolVersion: 1;
  requestId: string | null;
  category: ErrorCategory;
  retryable: boolean;
}

export type NativeResponse = AckResponse | CommandResultResponse | UploaderStatus | ErrorResponse;

export interface NativePortEvent<T> {
  addListener(listener: (value: T) => void): void;
  removeListener?(listener: (value: T) => void): void;
}

export interface NativePortLike {
  postMessage(message: NativeRequest): void;
  disconnect(): void;
  onMessage: NativePortEvent<unknown>;
  onDisconnect: NativePortEvent<void>;
}

export type NativePortFactory = () => NativePortLike;

export class NativeMessagingError extends Error {
  constructor(
    public readonly category: ErrorCategory,
    message: string = category,
    public readonly retryable = category === "host_unavailable",
  ) {
    super(message);
    this.name = "NativeMessagingError";
  }
}

export class NativeProtocolError extends NativeMessagingError {
  constructor(message = "invalid native messaging response") {
    super("protocol_mismatch", message, false);
    this.name = "NativeProtocolError";
  }
}

export class NativeRemoteError extends NativeMessagingError {
  constructor(response: ErrorResponse) {
    super(response.category, response.category, response.retryable);
    this.name = "NativeRemoteError";
  }
}

interface PendingRequest {
  resolve: (response: NativeResponse) => void;
  reject: (error: unknown) => void;
}

export interface NativeMessagingClientOptions {
  hostName?: string;
  portFactory?: NativePortFactory;
  requestIdFactory?: () => string;
}

const REQUEST_ID_PATTERN = /^[\x21-\x7e]{1,64}$/;
const HASH_PATTERN = /^[0-9a-f]{64}$/;
const LECTURE_KEY_PATTERN = /^[a-z0-9]+\/[0-9]{4}-(?:winter|spring|summer|fall)\/[0-9]{3}$/;
const RFC3339_SECONDS_PATTERN = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/;
const HTTPS_URL_PATTERN = /^https:\/\/[^\s]{1,2040}$/;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasExactKeys(value: Record<string, unknown>, required: readonly string[], optional: readonly string[] = []): boolean {
  const allowed = new Set([...required, ...optional]);
  const keys = Object.keys(value);
  return required.every((key) => Object.prototype.hasOwnProperty.call(value, key)) && keys.every((key) => allowed.has(key));
}

function isPrintableAscii(value: unknown, maxLength: number): value is string {
  return typeof value === "string" && value.length >= 1 && value.length <= maxLength && /^[\x20-\x7e]*$/.test(value);
}

function isPositiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function utf8Bytes(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}

export function isNativeTranscriptJob(value: unknown): value is NativeTranscriptJob {
  return validateTranscriptJob(value).valid;
}

export function isNativeRequest(value: unknown): value is NativeRequest {
  if (!isRecord(value) || value.protocolVersion !== PROTOCOL_VERSION || !isPrintableAscii(value.requestId, 64)) return false;
  switch (value.type) {
    case "connect":
      return hasExactKeys(value, ["type", "protocolVersion", "requestId", "extensionVersion"]) && isPrintableAscii(value.extensionVersion, 32);
    case "submit_job":
      return hasExactKeys(value, ["type", "protocolVersion", "requestId", "job"]) && isNativeTranscriptJob(value.job);
    case "status_request":
      return (
        hasExactKeys(value, ["type", "protocolVersion", "requestId"], ["beforeJobId", "limit"]) &&
        (value.beforeJobId === undefined || isPositiveInteger(value.beforeJobId)) &&
        (value.limit === undefined || (isPositiveInteger(value.limit) && value.limit <= MAX_STATUS_PAGE))
      );
    case "retry_job":
      return hasExactKeys(value, ["type", "protocolVersion", "requestId", "jobId"]) && isPositiveInteger(value.jobId);
    case "discard_job":
      return (
        hasExactKeys(value, ["type", "protocolVersion", "requestId", "jobId", "confirmation"]) &&
        isPositiveInteger(value.jobId) && value.confirmation === "discard"
      );
    case "reset":
      return hasExactKeys(value, ["type", "protocolVersion", "requestId"]);
    default:
      return false;
  }
}

function isRemoteFileKind(value: unknown): value is RemoteFileKind {
  return ["file", "directory", "symlink", "submodule", "malformed", "missing"].includes(value as RemoteFileKind);
}

function isJobSummary(value: unknown): value is JobSummary {
  if (!isRecord(value)) return false;
  const keys = [
    "jobId",
    "lectureKey",
    "contentHash",
    "status",
    "attemptCount",
    "nextAttemptAt",
    "updatedAt",
    "targetPath",
    "lastErrorCategory",
    "lastErrorHttpStatus",
    "remoteContentHash",
    "remoteFileKind",
  ] as const;
  return (
    hasExactKeys(value, keys) &&
    isPositiveInteger(value.jobId) &&
    typeof value.lectureKey === "string" && value.lectureKey.length <= 128 && LECTURE_KEY_PATTERN.test(value.lectureKey) &&
    typeof value.contentHash === "string" && HASH_PATTERN.test(value.contentHash) &&
    typeof value.status === "string" && (QUEUE_STATUSES as readonly string[]).includes(value.status) &&
    isNonNegativeInteger(value.attemptCount) &&
    (value.nextAttemptAt === null || (typeof value.nextAttemptAt === "string" && RFC3339_SECONDS_PATTERN.test(value.nextAttemptAt))) &&
    (value.updatedAt === null || (typeof value.updatedAt === "string" && RFC3339_SECONDS_PATTERN.test(value.updatedAt))) &&
    typeof value.targetPath === "string" && value.targetPath.length >= 1 && value.targetPath.length <= 512 &&
    (value.lastErrorCategory === null || (typeof value.lastErrorCategory === "string" && value.lastErrorCategory.length <= 64)) &&
    (value.lastErrorHttpStatus === null || (typeof value.lastErrorHttpStatus === "number" && Number.isInteger(value.lastErrorHttpStatus) && value.lastErrorHttpStatus >= 100 && value.lastErrorHttpStatus <= 599)) &&
    (value.remoteContentHash === null || (typeof value.remoteContentHash === "string" && HASH_PATTERN.test(value.remoteContentHash))) &&
    (value.remoteFileKind === null || isRemoteFileKind(value.remoteFileKind))
  );
}

function isAuthorizationStatus(value: unknown): boolean {
  if (!isRecord(value) || !hasExactKeys(value, ["userCode", "verificationUri", "verificationUriComplete", "expiresAt"])) return false;
  return (
    (value.userCode === null || isPrintableAscii(value.userCode, 64)) &&
    (value.verificationUri === null || (typeof value.verificationUri === "string" && value.verificationUri.length <= 2048 && HTTPS_URL_PATTERN.test(value.verificationUri))) &&
    (value.verificationUriComplete === null || (typeof value.verificationUriComplete === "string" && value.verificationUriComplete.length <= 2048 && HTTPS_URL_PATTERN.test(value.verificationUriComplete))) &&
    (value.expiresAt === null || (typeof value.expiresAt === "string" && RFC3339_SECONDS_PATTERN.test(value.expiresAt)))
  );
}

function isCountMap(value: unknown): value is CountMap {
  if (!isRecord(value) || !hasExactKeys(value, Object.keys(EMPTY_COUNTS))) return false;
  return Object.values(value).every((count) => isNonNegativeInteger(count));
}

export function isStatusResponse(value: unknown): value is UploaderStatus {
  if (!isRecord(value)) return false;
  const keys = [
    "type",
    "protocolVersion",
    "requestId",
    "extensionVersion",
    "uploaderVersion",
    "authState",
    "authorization",
    "drainState",
    "counts",
    "jobs",
    "nextBeforeJobId",
  ] as const;
  return (
    hasExactKeys(value, keys) &&
    value.type === "status" &&
    value.protocolVersion === PROTOCOL_VERSION &&
    (value.requestId === null || isPrintableAscii(value.requestId, 64)) &&
    isPrintableAscii(value.extensionVersion, 32) &&
    isPrintableAscii(value.uploaderVersion, 32) &&
    typeof value.authState === "string" && (AUTH_STATES as readonly string[]).includes(value.authState) &&
    isAuthorizationStatus(value.authorization) &&
    typeof value.drainState === "string" && (DRAIN_STATES as readonly string[]).includes(value.drainState) &&
    isCountMap(value.counts) &&
    Array.isArray(value.jobs) && value.jobs.length <= MAX_STATUS_PAGE && value.jobs.every(isJobSummary) &&
    (value.nextBeforeJobId === null || isPositiveInteger(value.nextBeforeJobId))
  );
}

export function isNativeResponse(value: unknown): value is NativeResponse {
  if (!isRecord(value) || value.protocolVersion !== PROTOCOL_VERSION || typeof value.type !== "string") return false;
  if (value.type === "status") return isStatusResponse(value);
  if (value.type === "error") {
    return (
      hasExactKeys(value, ["type", "protocolVersion", "requestId", "category", "retryable"]) &&
      (value.requestId === null || isPrintableAscii(value.requestId, 64)) &&
      typeof value.category === "string" && (ERROR_CATEGORIES as readonly string[]).includes(value.category) &&
      typeof value.retryable === "boolean"
    );
  }
  if (value.type === "ack") {
    return (
      hasExactKeys(value, ["type", "protocolVersion", "requestId", "operation", "jobId", "lectureKey", "contentHash", "status", "existingStatus", "action"]) &&
      typeof value.requestId === "string" && isPrintableAscii(value.requestId, 64) &&
      value.operation === "submit" &&
      (value.jobId === null || isPositiveInteger(value.jobId)) &&
      (value.lectureKey === null || (typeof value.lectureKey === "string" && value.lectureKey.length <= 128)) &&
      (value.contentHash === null || (typeof value.contentHash === "string" && HASH_PATTERN.test(value.contentHash))) &&
      typeof value.status === "string" && (SUBMIT_ACK_STATUSES as readonly string[]).includes(value.status) &&
      (value.existingStatus === null || (typeof value.existingStatus === "string" && (QUEUE_STATUSES as readonly string[]).includes(value.existingStatus))) &&
      (value.action === null || value.action === "retry_existing" || value.action === "discard_existing_then_recapture")
    );
  }
  if (value.type === "command_result") {
    return (
      hasExactKeys(value, ["type", "protocolVersion", "requestId", "operation", "jobId", "result", "status", "errorCategory"]) &&
      typeof value.requestId === "string" && isPrintableAscii(value.requestId, 64) &&
      (value.operation === "retry" || value.operation === "discard" || value.operation === "reset") &&
      (value.jobId === null || isPositiveInteger(value.jobId)) &&
      (value.result === "accepted" || value.result === "rejected") &&
      (value.status === null || value.status === "discarded" || value.status === "reset" || (typeof value.status === "string" && (QUEUE_STATUSES as readonly string[]).includes(value.status))) &&
      (value.errorCategory === null || (typeof value.errorCategory === "string" && (ERROR_CATEGORIES as readonly string[]).includes(value.errorCategory)))
    );
  }
  return false;
}

export function createRequestId(): string {
  const randomUuid = (globalThis.crypto as Crypto | undefined)?.randomUUID?.();
  if (randomUuid) return randomUuid;
  return `lt-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 14)}`.slice(0, 64);
}

export function makeConnectRequest(extensionVersion: string, requestId = createRequestId()): ConnectRequest {
  const request: ConnectRequest = { type: "connect", protocolVersion: PROTOCOL_VERSION, requestId, extensionVersion };
  assertRequest(request);
  return request;
}

export function makeSubmitJobRequest(job: NativeTranscriptJob, requestId = createRequestId()): SubmitJobRequest {
  const request: SubmitJobRequest = { type: "submit_job", protocolVersion: PROTOCOL_VERSION, requestId, job };
  assertRequest(request);
  return request;
}

export function makeStatusRequest(beforeJobId?: number, limit = MAX_STATUS_PAGE, requestId = createRequestId()): StatusRequest {
  const request: StatusRequest = { type: "status_request", protocolVersion: PROTOCOL_VERSION, requestId, limit };
  if (beforeJobId !== undefined) request.beforeJobId = beforeJobId;
  assertRequest(request);
  return request;
}

export function makeRetryJobRequest(jobId: number, requestId = createRequestId()): RetryJobRequest {
  const request: RetryJobRequest = { type: "retry_job", protocolVersion: PROTOCOL_VERSION, requestId, jobId };
  assertRequest(request);
  return request;
}

export function makeDiscardJobRequest(jobId: number, requestId = createRequestId()): DiscardJobRequest {
  const request: DiscardJobRequest = { type: "discard_job", protocolVersion: PROTOCOL_VERSION, requestId, jobId, confirmation: "discard" };
  assertRequest(request);
  return request;
}

export function makeResetRequest(requestId = createRequestId()): ResetRequest {
  const request: ResetRequest = { type: "reset", protocolVersion: PROTOCOL_VERSION, requestId };
  assertRequest(request);
  return request;
}

function assertRequest(request: NativeRequest): void {
  if (!isNativeRequest(request)) throw new TypeError("invalid native messaging request");
}

function defaultPortFactory(): NativePortLike {
  const chromeApi = (globalThis as unknown as { chrome?: { runtime?: { connectNative?: (name: string) => NativePortLike } } }).chrome;
  if (!chromeApi?.runtime?.connectNative) throw new NativeMessagingError("host_unavailable");
  return chromeApi.runtime.connectNative(NATIVE_HOST_NAME);
}

export class NativeMessagingClient {
  private port: NativePortLike | null = null;
  private connecting: Promise<NativePortLike> | null = null;
  private readonly pending = new Map<string, PendingRequest>();
  private readonly statusListeners = new Set<(status: UploaderStatus) => void>();
  private readonly disconnectListeners = new Set<() => void>();
  private readonly portFactory: NativePortFactory;
  private readonly requestIdFactory: () => string;

  constructor(private readonly options: NativeMessagingClientOptions = {}) {
    this.portFactory = options.portFactory ?? defaultPortFactory;
    this.requestIdFactory = options.requestIdFactory ?? createRequestId;
  }

  isConnected(): boolean {
    return this.port !== null;
  }

  onStatus(listener: (status: UploaderStatus) => void): () => void {
    this.statusListeners.add(listener);
    return () => this.statusListeners.delete(listener);
  }

  onDisconnect(listener: () => void): () => void {
    this.disconnectListeners.add(listener);
    return () => this.disconnectListeners.delete(listener);
  }

  async ensureConnected(): Promise<NativePortLike> {
    if (this.port) return this.port;
    if (this.connecting) return this.connecting;
    this.connecting = Promise.resolve().then(() => {
      const port = this.portFactory();
      this.attachPort(port);
      return port;
    }).catch((error: unknown) => {
      if (error instanceof NativeMessagingError) throw error;
      throw new NativeMessagingError("host_unavailable");
    }).finally(() => {
      this.connecting = null;
    });
    return this.connecting;
  }

  async connect(extensionVersion: string): Promise<UploaderStatus> {
    return this.request(makeConnectRequest(extensionVersion, this.requestIdFactory())) as Promise<UploaderStatus>;
  }

  async submitJob(job: NativeTranscriptJob): Promise<AckResponse> {
    return this.request(makeSubmitJobRequest(job, this.requestIdFactory())) as Promise<AckResponse>;
  }

  async statusRequest(beforeJobId?: number, limit = MAX_STATUS_PAGE): Promise<UploaderStatus> {
    return this.request(makeStatusRequest(beforeJobId, limit, this.requestIdFactory())) as Promise<UploaderStatus>;
  }

  async retryJob(jobId: number): Promise<CommandResultResponse> {
    return this.request(makeRetryJobRequest(jobId, this.requestIdFactory())) as Promise<CommandResultResponse>;
  }

  async discardJob(jobId: number): Promise<CommandResultResponse> {
    return this.request(makeDiscardJobRequest(jobId, this.requestIdFactory())) as Promise<CommandResultResponse>;
  }

  async reset(): Promise<CommandResultResponse> {
    return this.request(makeResetRequest(this.requestIdFactory())) as Promise<CommandResultResponse>;
  }

  async request(request: NativeRequest): Promise<NativeResponse> {
    assertRequest(request);
    const port = await this.ensureConnected();
    return new Promise<NativeResponse>((resolve, reject) => {
      this.pending.set(request.requestId, { resolve, reject });
      try {
        port.postMessage(request);
      } catch {
        this.pending.delete(request.requestId);
        reject(new NativeMessagingError("host_unavailable"));
      }
    });
  }

  disconnect(): void {
    const port = this.port;
    if (!port) return;
    this.port = null;
    try {
      port.disconnect();
    } catch {
      // A port may already have been closed by Chrome.
    }
    this.rejectPending(new NativeMessagingError("host_unavailable"));
  }

  private attachPort(port: NativePortLike): void {
    this.port = port;
    port.onMessage.addListener((message) => this.handleMessage(message));
    port.onDisconnect.addListener(() => this.handleDisconnect(port));
  }

  private handleMessage(message: unknown): void {
    if (!isNativeResponse(message)) {
      this.rejectPending(new NativeProtocolError());
      return;
    }
    if (message.type === "status") {
      for (const listener of this.statusListeners) listener(message);
    }
    if (message.type === "error") {
      if (message.requestId !== null) {
        const pending = this.pending.get(message.requestId);
        if (pending) {
          this.pending.delete(message.requestId);
          pending.reject(new NativeRemoteError(message));
        }
      }
      return;
    }
    if (message.requestId === null) return;
    const pending = this.pending.get(message.requestId);
    if (!pending) return;
    this.pending.delete(message.requestId);
    pending.resolve(message);
  }

  private handleDisconnect(port: NativePortLike): void {
    if (this.port !== port) return;
    this.port = null;
    this.rejectPending(new NativeMessagingError("host_unavailable"));
    for (const listener of this.disconnectListeners) listener();
  }

  private rejectPending(error: NativeMessagingError): void {
    for (const { reject } of this.pending.values()) reject(error);
    this.pending.clear();
  }
}
