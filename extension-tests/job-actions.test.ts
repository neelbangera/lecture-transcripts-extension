import { describe, expect, it } from "vitest";

import {
  canDiscardJob,
  canRetryJob,
  clearableJobs,
  clearUploadedConfirmText,
  discardConfirmText,
  isClearableStatus,
} from "../extension/src/job-actions";
import {
  QUEUE_STATUSES,
  type JobSummary,
  type QueueStatus,
} from "../extension/src/status";

function summary(jobId: number, status: QueueStatus): JobSummary {
  return {
    jobId,
    lectureKey: `eecs484/2026-fall/${String(jobId).padStart(3, "0")}`,
    contentHash: "a".repeat(64),
    status,
    attemptCount: 0,
    nextAttemptAt: null,
    updatedAt: null,
    targetPath: `eecs484/${String(jobId).padStart(3, "0")}.md`,
    lastErrorCategory: null,
    lastErrorHttpStatus: null,
    remoteContentHash: null,
    remoteFileKind: null,
    lectureDate: "2026-09-01",
    displayTitle: "Intro, Smith",
  };
}

describe("popup job actions", () => {
  it("treats only uploaded and unchanged rows as clearable", () => {
    for (const status of QUEUE_STATUSES) {
      expect(isClearableStatus(status)).toBe(
        status === "uploaded" || status === "unchanged",
      );
    }
  });

  it("allows discard for terminal rows without allowing queued or uploading rows", () => {
    const discardable: QueueStatus[] = [
      "uploaded",
      "unchanged",
      "permanent_conflict",
      ...QUEUE_STATUSES.filter((status) => status.startsWith("rejected_")),
    ];
    const blocked: QueueStatus[] = [
      "queued",
      "uploading",
      "retryable_error",
    ];
    for (const status of discardable) {
      expect(canDiscardJob(status), status).toBe(true);
    }
    for (const status of blocked) {
      expect(canDiscardJob(status), status).toBe(false);
    }
  });

  it("keeps retry eligibility unchanged", () => {
    expect(canRetryJob("retryable_error")).toBe(true);
    expect(canRetryJob("permanent_conflict")).toBe(true);
    expect(canRetryJob("rejected_permission")).toBe(true);
    expect(canRetryJob("uploaded")).toBe(false);
    expect(canRetryJob("unchanged")).toBe(false);
    expect(canRetryJob("queued")).toBe(false);
  });

  it("selects only the loaded uploaded and unchanged rows to clear", () => {
    const jobs = [
      summary(1, "uploaded"),
      summary(2, "queued"),
      summary(3, "unchanged"),
      summary(4, "permanent_conflict"),
      summary(5, "uploading"),
    ];

    expect(clearableJobs(jobs).map((job) => job.jobId)).toEqual([1, 3]);
  });

  it("spells out that discard never touches a GitHub file", () => {
    const text = discardConfirmText(summary(1, "uploaded"));
    expect(text).toContain("untouched");
    expect(text).toContain("never deletes a remote file");
    expect(text).toContain("eecs484/2026-fall/001");

    expect(clearUploadedConfirmText(1)).toContain("1 local uploaded row");
    expect(clearUploadedConfirmText(2)).toContain("2 local uploaded rows");
    expect(clearUploadedConfirmText(2)).toContain("untouched");
    expect(clearUploadedConfirmText(2)).toContain("never deletes a remote file");
  });
});
