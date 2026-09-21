/**
 * Shared extension-facing status vocabulary.
 *
 * These values deliberately mirror the Native Messaging contract in
 * TECHNICAL_PLAN.md.  Keep the display text here so the popup and page-facing
 * status cannot quietly grow separate vocabularies.
 */

export const QUEUE_STATUSES = [
  "queued",
  "uploading",
  "uploaded",
  "unchanged",
  "retryable_error",
  "permanent_conflict",
  "rejected_missing_identity",
  "rejected_ambiguous_metadata",
  "rejected_oversized",
  "rejected_queue_full",
  "rejected_invalid_hash",
  "rejected_unsafe_url",
  "rejected_unknown_field",
  "rejected_invalid_schema",
  "rejected_permission",
] as const;

export type QueueStatus = (typeof QUEUE_STATUSES)[number];

export const EXTENSION_LOCAL_STATUSES = [
  "rejected_handoff_full",
  "not_ready",
  "waiting_for_uploader",
] as const;

export type ExtensionLocalStatus = (typeof EXTENSION_LOCAL_STATUSES)[number];

export const AUTH_STATES = [
  "not_connected",
  "authorizing",
  "connected",
  "reauthorization_required",
  "target_repository_unavailable",
  "protocol_mismatch",
] as const;

export type AuthState = (typeof AUTH_STATES)[number];

export const DRAIN_STATES = [
  "idle",
  "working",
  "waiting_for_backoff",
  "authorizing",
] as const;

export type DrainState = (typeof DRAIN_STATES)[number];

export const ERROR_CATEGORIES = [
  "protocol_mismatch",
  "invalid_message",
  "host_unavailable",
  "invalid_state",
  "not_connected",
  "reauthorization_required",
  "target_repository_unavailable",
  "internal",
  "ineligible_command",
  "rejected_missing_identity",
  "rejected_ambiguous_metadata",
  "rejected_oversized",
  "rejected_queue_full",
  "rejected_handoff_full",
  "rejected_invalid_hash",
  "rejected_unsafe_url",
  "rejected_unknown_field",
  "rejected_invalid_schema",
  "rejected_permission",
] as const;

export type ErrorCategory = (typeof ERROR_CATEGORIES)[number];

export type CountMap = Record<QueueStatus, number>;

export const EMPTY_COUNTS: CountMap = Object.freeze({
  queued: 0,
  uploading: 0,
  uploaded: 0,
  unchanged: 0,
  retryable_error: 0,
  permanent_conflict: 0,
  rejected_missing_identity: 0,
  rejected_ambiguous_metadata: 0,
  rejected_oversized: 0,
  rejected_queue_full: 0,
  rejected_invalid_hash: 0,
  rejected_unsafe_url: 0,
  rejected_unknown_field: 0,
  rejected_invalid_schema: 0,
  rejected_permission: 0,
});

export type RemoteFileKind =
  | "file"
  | "directory"
  | "symlink"
  | "submodule"
  | "malformed"
  | "missing";

export interface JobSummary {
  jobId: number;
  lectureKey: string;
  contentHash: string;
  status: QueueStatus;
  attemptCount: number;
  nextAttemptAt: string | null;
  updatedAt: string | null;
  targetPath: string;
  lastErrorCategory: string | null;
  lastErrorHttpStatus: number | null;
  remoteContentHash: string | null;
  remoteFileKind: RemoteFileKind | null;
}

export interface AuthorizationStatus {
  userCode: string | null;
  verificationUri: string | null;
  verificationUriComplete: string | null;
  expiresAt: string | null;
}

export interface UploaderStatus {
  type: "status";
  protocolVersion: 1;
  requestId: string | null;
  extensionVersion: string;
  uploaderVersion: string;
  authState: AuthState;
  authorization: AuthorizationStatus;
  drainState: DrainState;
  counts: CountMap;
  jobs: JobSummary[];
  nextBeforeJobId: number | null;
}

export interface OverflowNotice {
  lectureKey: string;
  lectureDate: string;
  capturedAt: string;
  reason:
    | "rejected_handoff_full"
    | "rejected_queue_full"
    | "rejected_duplicate_terminal"
    | "rejected_invalid_schema"
    | "rejected_oversized"
    | "rejected_invalid_hash"
    | "rejected_unsafe_url"
    | "rejected_unknown_field"
    | "rejected_permission";
}

export interface ExtensionSnapshot {
  uploader: UploaderStatus | null;
  pendingHandoffs: number;
  overflowNotices: OverflowNotice[];
  lastOutcome: LastOutcome | null;
  updatedAt: string;
}

export interface LastOutcome {
  status: string;
  lectureKey: string | null;
  contentHash: string | null;
  message: string;
  action: "none" | "retry_existing" | "discard_existing_then_recapture";
  at: string;
}

const LABELS: Record<string, string> = {
  queued: "Queued for upload",
  uploading: "Uploading",
  uploaded: "Uploaded",
  unchanged: "Already uploaded; unchanged",
  retryable_error: "Temporary error; will retry",
  permanent_conflict: "Conflict — manual review needed",
  rejected_missing_identity: "Rejected — missing lecture identity",
  rejected_ambiguous_metadata: "Rejected — ambiguous lecture metadata",
  rejected_oversized: "Rejected — transcript is too large",
  rejected_queue_full: "Rejected — uploader queue is full",
  rejected_handoff_full: "Rejected — pending handoff is full",
  rejected_invalid_hash: "Rejected — invalid transcript hash",
  rejected_unsafe_url: "Rejected — unsafe source URL",
  rejected_unknown_field: "Rejected — unknown field",
  rejected_invalid_schema: "Rejected — invalid job schema",
  rejected_permission: "Rejected — GitHub permission denied",
  not_ready: "Transcript is not ready yet",
  waiting_for_uploader: "Waiting for local uploader",
  not_connected: "GitHub is not connected",
  authorizing: "Finish GitHub authorization",
  connected: "GitHub connected",
  reauthorization_required: "Reconnect GitHub",
  target_repository_unavailable: "Target repository is unavailable",
  protocol_mismatch: "Extension and uploader versions are incompatible",
  host_unavailable: "Local uploader is unavailable",
  invalid_message: "Uploader rejected an invalid message",
  invalid_state: "Uploader is in an invalid state",
  ineligible_command: "That action is not available for this job",
  internal: "Local uploader error",
};

export function statusLabel(status: string): string {
  return LABELS[status] ?? "Unknown status";
}

export function errorLabel(category: string): string {
  return LABELS[category] ?? statusLabel(category);
}

export function isQueueStatus(value: unknown): value is QueueStatus {
  return typeof value === "string" && (QUEUE_STATUSES as readonly string[]).includes(value);
}

export function isAuthState(value: unknown): value is AuthState {
  return typeof value === "string" && (AUTH_STATES as readonly string[]).includes(value);
}

export function isDrainState(value: unknown): value is DrainState {
  return typeof value === "string" && (DRAIN_STATES as readonly string[]).includes(value);
}

export function isErrorCategory(value: unknown): value is ErrorCategory {
  return typeof value === "string" && (ERROR_CATEGORIES as readonly string[]).includes(value);
}

export function cloneCounts(counts: CountMap | undefined): CountMap {
  return { ...EMPTY_COUNTS, ...(counts ?? {}) };
}

