import { describe, expect, it } from "vitest";

import {
  MAX_OTHER_STRING_CHARACTERS,
  MAX_SOURCE_URL_BYTES,
  MAX_TRANSCRIPT_BYTES,
  SourceUrlError,
  canonicalizeSourceUrl,
  createTranscriptJob,
  deriveLectureKey,
  deriveStableLecturePath,
  sanitizeSourceUrlForPublish,
  serializeTranscriptJob,
  sourceUrlInfo,
  validateTranscriptJob,
} from "../extension/src/transcript-job";

const BASE_INPUT = {
  courseName: "EECS 484",
  term: "Fall 2026",
  lectureNumber: 1,
  lectureDate: "2026-09-01",
  sourceUrl: "https://LECCAP.ENGIN.UMICH.EDU:443/leccap/player/r/sanitized01?session=removed#fragment",
  capturedAt: "2026-09-20T12:34:56.789Z",
  transcript:
    "Can I ask you a question?\n" +
    "I think we can start. Welcome in 484,\n" +
    "Database Management System.\n" +
    "Thank you.",
  timestampedTranscript:
    "[00:00] Can I ask you a question?\n" +
    "[00:02] I think we can start. Welcome in 484,\n" +
    "Database Management System.\n" +
    "[1:22:58] Thank you.",
} as const;

function expectInvalid(value: unknown, code: string): void {
  const result = validateTranscriptJob(value);
  expect(result.valid).toBe(false);
  if (result.valid) {
    throw new Error("expected invalid TranscriptJob");
  }
  expect(result.error.code).toBe(code);
}

describe("TranscriptJob", () => {
  it("copies only the committed course mapping and derives identity/path", () => {
    const job = createTranscriptJob(BASE_INPUT);
    expect(job.courseSlug).toBe("eecs484");
    expect(job.term).toBe("2026-fall");
    expect(job.lectureKey).toBe("eecs484/2026-fall/001");
    expect(deriveLectureKey("eecs484", "Fall 2026", 6)).toBe("eecs484/2026-fall/006");
    expect(deriveStableLecturePath("eecs484", "2026-fall", 6)).toBe(
      "courses/eecs484/2026-fall/lectures/006.md",
    );
  });

  it("normalizes capturedAt to whole-second UTC and computes the committed hash", () => {
    const job = createTranscriptJob(BASE_INPUT);
    expect(job.capturedAt).toBe("2026-09-20T12:34:56Z");
    expect(job.sourceUrl).toBe("https://leccap.engin.umich.edu/leccap/player/r/sanitized01");
    expect(job.contentHash).toBe(
      "4bfc0f5736615f920fa7ad0f557ff1c20426f39525bfc8717f4d9f2a770ed0cd",
    );
    expect(serializeTranscriptJob(job)).toBe(JSON.stringify(job));
  });

  it("canonicalizes only the transport-safe URL components", () => {
    expect(canonicalizeSourceUrl("HTTPS://LECCAP.ENGIN.UMICH.EDU:443/a/B/?q=x#fragment")).toBe(
      "https://leccap.engin.umich.edu/a/B/",
    );
    expect(canonicalizeSourceUrl("https://leccap.engin.umich.edu")).toBe(
      "https://leccap.engin.umich.edu/",
    );
    expect(sourceUrlInfo("https://leccap.engin.umich.edu/a/b?token=secret#x")).toEqual({
      canonicalUrl: "https://leccap.engin.umich.edu/a/b",
      publishUrl: "https://leccap.engin.umich.edu/a/b",
      logValue: "leccap.engin.umich.edu/a/b",
    });
  });

  it("omits sensitive path segments only at publish time", () => {
    expect(sanitizeSourceUrlForPublish("https://leccap.engin.umich.edu/recording/auth")).toBeNull();
    expect(sanitizeSourceUrlForPublish("https://leccap.engin.umich.edu/recording/session-id")).toBeNull();
    expect(sanitizeSourceUrlForPublish("https://leccap.engin.umich.edu/recording/token_value")).toBeNull();
    expect(sanitizeSourceUrlForPublish("https://leccap.engin.umich.edu/recording/author")).toBe(
      "https://leccap.engin.umich.edu/recording/author",
    );
  });

  it("rejects unsafe URL schemes, hosts, userinfo, ports, and canonical overlength", () => {
    expect(() => canonicalizeSourceUrl("http://leccap.engin.umich.edu/a")).toThrow(SourceUrlError);
    expect(() => canonicalizeSourceUrl("https://example.com/a")).toThrow(SourceUrlError);
    expect(() => canonicalizeSourceUrl("https://user:pass@leccap.engin.umich.edu/a")).toThrow(
      SourceUrlError,
    );
    expect(() => canonicalizeSourceUrl("https://leccap.engin.umich.edu:444/a")).toThrow(
      SourceUrlError,
    );
    const longPath = "a".repeat(MAX_SOURCE_URL_BYTES);
    expect(() => canonicalizeSourceUrl(`https://leccap.engin.umich.edu/${longPath}`)).toThrow(
      SourceUrlError,
    );
  });

  it("rejects unknown fields and invalid hashes", () => {
    const job = createTranscriptJob(BASE_INPUT);
    expectInvalid({ ...job, unexpected: true }, "rejected_unknown_field");
    expectInvalid({ ...job, contentHash: "A".repeat(64) }, "rejected_invalid_hash");
    expectInvalid({ ...job, contentHash: "0".repeat(64) }, "rejected_invalid_hash");
  });

  it("rejects noncanonical identity, dates, timestamps, and transcript text", () => {
    const job = createTranscriptJob(BASE_INPUT);
    expectInvalid({ ...job, term: "Fall 2026" }, "rejected_invalid_schema");
    expectInvalid({ ...job, lectureKey: "eecs484/2026-fall/002" }, "rejected_invalid_schema");
    expectInvalid({ ...job, lectureDate: "2026-02-30" }, "rejected_invalid_schema");
    expectInvalid({ ...job, capturedAt: "2026-09-20T12:34:56.123Z" }, "rejected_invalid_schema");
    expectInvalid({ ...job, transcript: "x".repeat(50) + "\n" }, "rejected_invalid_schema");
    expectInvalid({ ...job, sourceUrl: `${job.sourceUrl}?unsafe=query` }, "rejected_unsafe_url");
  });

  it("enforces the transcript and bounded metadata sizes", () => {
    const job = createTranscriptJob(BASE_INPUT);
    expectInvalid(
      { ...job, transcript: "x".repeat(MAX_TRANSCRIPT_BYTES + 1) },
      "rejected_oversized",
    );
    expectInvalid(
      { ...job, courseName: "x".repeat(MAX_OTHER_STRING_CHARACTERS + 1) },
      "rejected_oversized",
    );
  });
});
