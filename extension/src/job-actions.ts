import type { JobSummary, QueueStatus } from "./status";

export const CLEARABLE_STATUSES = ["uploaded", "unchanged"] as const;

export function isClearableStatus(status: QueueStatus): boolean {
  return (CLEARABLE_STATUSES as readonly string[]).includes(status);
}

export function canDiscardJob(status: QueueStatus): boolean {
  return status === "permanent_conflict" || status.startsWith("rejected_") || isClearableStatus(status);
}

export function canRetryJob(status: QueueStatus): boolean {
  return status === "retryable_error" || status === "permanent_conflict" || status === "rejected_permission";
}

export function clearableJobs(jobs: readonly JobSummary[]): JobSummary[] {
  return jobs.filter((job) => isClearableStatus(job.status));
}

export function discardConfirmText(job: JobSummary): string {
  return `Discard the local ${job.lectureKey} row? The GitHub file is untouched; this never deletes a remote file.`;
}

export function clearUploadedConfirmText(count: number): string {
  const rows = count === 1 ? "row" : "rows";
  return `Discard ${count} local uploaded ${rows}? GitHub files are untouched; this never deletes a remote file.`;
}
