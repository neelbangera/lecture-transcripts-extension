import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

import normalizationVectors from "../protocol/normalization-vectors.json";
import sourceUrlVectors from "../protocol/source-url-vectors.json";
import {
  computeNormalizedContentHash,
  frameTranscriptHashInput,
  normalizeTranscript,
} from "../extension/src/transcript-normalizer";
import {
  MAX_SERIALIZED_JOB_BYTES,
  SourceUrlError,
  canonicalizeSourceUrl,
  createTranscriptJob,
  sanitizeSourceUrlForPublish,
  serializeTranscriptJob,
  transcriptJobByteLength,
  validateTranscriptJob,
  type TranscriptJobInput,
} from "../extension/src/transcript-job";

interface NormalizationVector {
  id: string;
  plainInput: string;
  timestampedInput: string;
  expectedPlain: string;
  expectedTimestamped: string;
  hashAssertion: string;
}

interface SourceUrlVector {
  id: string;
  input: string;
  expectedCanonicalUrl: string | null;
  expectedPublishSourceUrl: string | null;
  expectedAction: "include" | "omit" | "reject";
  expectedError?: string;
}

const vectors = normalizationVectors as NormalizationVector[];
const urlVectors = sourceUrlVectors as SourceUrlVector[];
const textEncoder = new TextEncoder();

const HASH_VERSION_PREFIX = "transcript-hash-v1\0";

const FIXTURE_INPUTS: TranscriptJobInput = {
  courseName: "EECS 484",
  term: "Fall 2026",
  lectureNumber: 1,
  lectureDate: "2026-09-01",
  sourceUrl: "https://LECCAP.ENGIN.UMICH.EDU:443/leccap/player/r/sanitized01?session=removed#fragment",
  capturedAt: "2026-09-20T12:34:56.789Z",
  transcript:
    "Can I ask you a question?\n" +
    "I think we can start. Welcome to EECS 484.\n" +
    "We cover SQL, indexes & query plans.\n" +
    "If x < y, the plan uses a range scan.\n" +
    "Q&A follows the lecture.",
  timestampedTranscript:
    "[00:00] Can I ask you a question?\n" +
    "[00:02] I think we can start. Welcome to EECS 484.\n" +
    "We cover SQL, indexes & query plans.\n" +
    "If x < y, the plan uses a range scan.\n" +
    "[1:22:58] Q&A follows the lecture.",
};

const fixturePath = join(
  dirname(fileURLToPath(import.meta.url)),
  "..",
  "uploader",
  "internal",
  "protocol",
  "testdata",
  "transcript-job.canonical.json",
);
const fixture = readFileSync(fixturePath, "utf8");

describe("protocol normalization vectors", () => {
  const hashes = new Map<string, string>(
    vectors.map((vector) => [
      vector.id,
      computeNormalizedContentHash(vector.expectedPlain, vector.expectedTimestamped),
    ]),
  );

  it("produces the exact expected plain and timestamped outputs", () => {
    for (const vector of vectors) {
      expect(normalizeTranscript(vector.plainInput), vector.id).toBe(vector.expectedPlain);
      expect(normalizeTranscript(vector.timestampedInput), vector.id).toBe(
        vector.expectedTimestamped,
      );
    }
  });

  it("treats the expected outputs as normalized fixed points", () => {
    for (const vector of vectors) {
      expect(normalizeTranscript(vector.expectedPlain), vector.id).toBe(vector.expectedPlain);
      expect(normalizeTranscript(vector.expectedTimestamped), vector.id).toBe(
        vector.expectedTimestamped,
      );
    }
  });

  it("frames the exact UTF-8 byte lengths used by the hash", () => {
    for (const vector of vectors) {
      const plainBytes = textEncoder.encode(vector.expectedPlain);
      const timestampedBytes = textEncoder.encode(vector.expectedTimestamped);
      const frame = frameTranscriptHashInput(vector.expectedPlain, vector.expectedTimestamped);
      const expectedFrame = new Uint8Array([
        ...textEncoder.encode(HASH_VERSION_PREFIX),
        ...textEncoder.encode(`${plainBytes.length}:`),
        ...plainBytes,
        ...textEncoder.encode(`${timestampedBytes.length}:`),
        ...timestampedBytes,
      ]);
      expect(Array.from(frame), vector.id).toEqual(Array.from(expectedFrame));
      expect(frame.length, vector.id).toBe(expectedFrame.length);
    }
  });

  it("reproduces every declared hash relationship", () => {
    for (const vector of vectors) {
      const ownHash = hashes.get(vector.id);
      expect(ownHash, vector.id).toMatch(/^[0-9a-f]{64}$/);

      if (vector.hashAssertion === "normalized-self") {
        expect(
          computeNormalizedContentHash(vector.expectedPlain, vector.expectedTimestamped),
          vector.id,
        ).toBe(ownHash);
        continue;
      }
      if (vector.hashAssertion.startsWith("different-from:")) {
        const other = vector.hashAssertion.slice("different-from:".length);
        expect(hashes.get(other), `${vector.id} -> ${other}`).toBeDefined();
        expect(ownHash, vector.id).not.toBe(hashes.get(other));
        continue;
      }
      if (vector.hashAssertion.startsWith("same-as:")) {
        const other = vector.hashAssertion.slice("same-as:".length);
        expect(hashes.get(other), `${vector.id} -> ${other}`).toBeDefined();
        expect(ownHash, vector.id).toBe(hashes.get(other));
        continue;
      }
      throw new Error(`unknown hash assertion ${vector.hashAssertion} for ${vector.id}`);
    }
  });
});

describe("protocol source URL vectors", () => {
  it("canonicalizes or rejects every vector exactly", () => {
    for (const vector of urlVectors) {
      if (vector.expectedAction === "reject") {
        expect(() => canonicalizeSourceUrl(vector.input), vector.id).toThrow(SourceUrlError);
        continue;
      }
      expect(canonicalizeSourceUrl(vector.input), vector.id).toBe(vector.expectedCanonicalUrl);
      expect(sanitizeSourceUrlForPublish(vector.input), vector.id).toBe(
        vector.expectedPublishSourceUrl,
      );
    }
  });

  it("maps a rejected vector to rejected_unsafe_url in job validation", () => {
    const baseJob = createTranscriptJob(FIXTURE_INPUTS);
    for (const vector of urlVectors) {
      if (vector.expectedAction !== "reject") {
        continue;
      }
      const result = validateTranscriptJob({ ...baseJob, sourceUrl: vector.input });
      expect(result.valid, vector.id).toBe(false);
      if (!result.valid) {
        expect(result.error.code, vector.id).toBe(
          vector.expectedError ?? "rejected_unsafe_url",
        );
      }
    }
  });
});

describe("cross-language canonical job fixture", () => {
  it("is byte-for-byte what the TypeScript builder emits", () => {
    const job = createTranscriptJob(FIXTURE_INPUTS);
    expect(serializeTranscriptJob(job)).toBe(fixture);
    expect(transcriptJobByteLength(job)).toBe(Buffer.byteLength(fixture, "utf8"));
  });

  it("validates, recomputes its hash, and re-serializes unchanged", () => {
    const result = validateTranscriptJob(JSON.parse(fixture));
    expect(result.valid).toBe(true);
    if (!result.valid) {
      return;
    }
    expect(result.job.contentHash).toBe(
      computeNormalizedContentHash(result.job.transcript, result.job.timestampedTranscript),
    );
    expect(serializeTranscriptJob(result.job)).toBe(fixture);
    expect(Buffer.byteLength(fixture, "utf8")).toBeLessThanOrEqual(MAX_SERIALIZED_JOB_BYTES);
  });

  it("stays compact and keeps escaping characters raw for Go parity", () => {
    expect(fixture.endsWith("\n")).toBe(false);
    expect(fixture).not.toContain(": ");
    expect(fixture).toContain("&");
    expect(fixture).toContain("<");
    expect(fixture).not.toContain("\\u0026");
    expect(fixture).not.toContain("\\u003c");
    expect(fixture).not.toContain("\\u003e");
  });
});
