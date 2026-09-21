import { describe, expect, it } from "vitest";

import {
  computeContentHash,
  derivePlainTranscript,
  frameTranscriptHashInput,
  normalizeTranscript,
  normalizeTranscriptForms,
  sha256Hex,
  TIMESTAMP_PREFIX_RE,
} from "../extension/src/transcript-normalizer";

describe("transcript normalization", () => {
  it("normalizes NFC, line endings, horizontal whitespace, and blank lines", () => {
    expect(normalizeTranscript("Hello  world\r\n")).toBe("Hello world");
    expect(normalizeTranscript("a\n\n\n\nb")).toBe("a\n\nb");
    expect(normalizeTranscript("e\u0301lan")).toBe("élan");
    expect(normalizeTranscript("élan")).toBe("élan");
  });

  it("preserves timestamp digits and normalizes their separator", () => {
    expect(normalizeTranscript(" [00:01]   The   fox  ")).toBe(" [00:01] The fox");
    expect(normalizeTranscript("1:22:58 -  Thank you")).toBe("1:22:58 - Thank you");
    expect(normalizeTranscript("[00:01]")).toBe("[00:01]");
    expect(TIMESTAMP_PREFIX_RE.test("[00:01] The fox")).toBe(true);
    expect(TIMESTAMP_PREFIX_RE.test("1:22:58 Thank you")).toBe(true);
  });

  it("derives plain text from a timestamped-only source without inventing timestamps", () => {
    const timestamped = "[00:01] The fox\n[00:02] jumps";
    expect(derivePlainTranscript(timestamped)).toBe("The fox\njumps");
    expect(normalizeTranscriptForms({ timestampedTranscript: timestamped })).toEqual({
      transcript: "The fox\njumps",
      timestampedTranscript: timestamped,
      derivedFrom: "timestamped-only",
      contentHash: computeContentHash("The fox\njumps", timestamped),
    });
  });

  it("keeps plain-only jobs timestamp-free", () => {
    expect(normalizeTranscriptForms({ transcript: "The fox" })).toMatchObject({
      transcript: "The fox",
      timestampedTranscript: "",
      derivedFrom: "plain-only",
    });
  });

  it("uses the exact framed hash and counts UTF-8 bytes", () => {
    const framed = new TextDecoder().decode(frameTranscriptHashInput("é", ""));
    expect(framed).toBe("transcript-hash-v1\0" + "2:é0:");
    expect(sha256Hex(new TextEncoder().encode("abc"))).toBe(
      "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
    );
  });

  it("matches the committed valid lecture hash", () => {
    const transcript =
      "Can I ask you a question?\n" +
      "I think we can start. Welcome in 484,\n" +
      "Database Management System.\n" +
      "Thank you.";
    const timestampedTranscript =
      "[00:00] Can I ask you a question?\n" +
      "[00:02] I think we can start. Welcome in 484,\n" +
      "Database Management System.\n" +
      "[1:22:58] Thank you.";

    expect(computeContentHash(transcript, timestampedTranscript)).toBe(
      "4bfc0f5736615f920fa7ad0f557ff1c20426f39525bfc8717f4d9f2a770ed0cd",
    );
  });

  it("changes the hash for meaningful words, timestamp digits, and timestamp presence", () => {
    expect(computeContentHash("The fox", "")).not.toBe(computeContentHash("The dog", ""));
    expect(computeContentHash("The fox", "[00:01] The fox")).not.toBe(
      computeContentHash("The fox", "[00:02] The fox"),
    );
    expect(computeContentHash("The fox", "")).not.toBe(
      computeContentHash("The fox", "[00:01] The fox"),
    );
    expect(computeContentHash("e\u0301lan", "")).toBe(computeContentHash("élan", ""));
  });

  it("is idempotent after normalization", () => {
    const raw = "[00:01]  The   fox\r\n\r\n\r\nnext";
    const normalized = normalizeTranscript(raw);
    expect(normalizeTranscript(normalized)).toBe(normalized);
  });
});
