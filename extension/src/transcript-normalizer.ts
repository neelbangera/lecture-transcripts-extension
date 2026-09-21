/**
 * Shared transcript normalization and hashing rules.
 *
 * Keep this module browser-compatible: the extension bundle must not depend on
 * Node's crypto module.  The small synchronous SHA-256 implementation below
 * is used so job creation can remain deterministic and synchronous in the
 * content script as well as in tests.
 */

export const TIMESTAMP_PREFIX_SOURCE =
  String.raw`^\s*[\[(]?\d{1,2}:\d{2}(?::\d{2})?[\]\)]?\s*(?:[-–—|]\s*)?`;

/** The normative prefix matcher; it intentionally has no global flag. */
export const TIMESTAMP_PREFIX_RE = new RegExp(TIMESTAMP_PREFIX_SOURCE);

export type TranscriptDerivedFrom = "both" | "timestamped-only" | "plain-only";

export interface TranscriptFormsInput {
  readonly transcript?: string;
  readonly timestampedTranscript?: string;
}

export interface NormalizedTranscriptForms {
  readonly transcript: string;
  readonly timestampedTranscript: string;
  readonly derivedFrom: TranscriptDerivedFrom;
  readonly contentHash: string;
}

const textEncoder = new TextEncoder();
const HASH_VERSION_PREFIX = "transcript-hash-v1\0";

function requireString(value: unknown, fieldName: string): string {
  if (typeof value !== "string") {
    throw new TypeError(`${fieldName} must be a string`);
  }
  return value;
}

function normalizeLineBody(line: string): string {
  return line.replace(/[ \t]+$/, "").replace(/[ \t]{2,}/g, " ");
}

/**
 * Normalize a transcript exactly as specified by TECHNICAL_PLAN.md.
 * Timestamp prefixes are retained, including their timestamp digits and
 * punctuation; only their separator whitespace is made deterministic.
 */
export function normalizeTranscript(value: string): string {
  const nfc = requireString(value, "transcript").normalize("NFC");
  const lf = nfc.replace(/\r\n?/g, "\n");

  const lines = lf.split("\n").map((line) => {
    const match = TIMESTAMP_PREFIX_RE.exec(line);
    if (!match) {
      return normalizeLineBody(line);
    }

    // The prefix's timestamp digits are deliberately not parsed or rewritten.
    // Removing only trailing horizontal whitespace keeps the prefix verbatim
    // while the rejoin below gives it one deterministic separator.
    const prefix = match[0].replace(/[ \t]+$/, "");
    const rest = normalizeLineBody(line.slice(match[0].length));

    if (!rest) {
      return prefix;
    }
    return `${prefix} ${rest}`;
  });

  return lines.join("\n").replace(/\n{3,}/g, "\n\n").replace(/^\n+|\n+$/g, "");
}

/** Remove one normative timestamp prefix without changing the remainder. */
export function stripTimestampPrefix(line: string): string {
  const value = requireString(line, "line");
  const match = TIMESTAMP_PREFIX_RE.exec(value);
  return match ? value.slice(match[0].length) : value;
}

/** Derive plain text from a timestamped-only source, then normalize it. */
export function derivePlainTranscript(timestampedTranscript: string): string {
  const source = requireString(timestampedTranscript, "timestampedTranscript");
  const nfc = source.normalize("NFC").replace(/\r\n?/g, "\n");
  return normalizeTranscript(nfc.split("\n").map(stripTimestampPrefix).join("\n"));
}

/**
 * Normalize the two forms independently and apply the explicit derivation
 * policy.  Presence of a property is significant for the in-memory
 * `derivedFrom` value; the wire job always carries both string fields.
 */
export function normalizeTranscriptForms(input: TranscriptFormsInput): NormalizedTranscriptForms {
  const hasPlain = Object.prototype.hasOwnProperty.call(input, "transcript");
  const hasTimestamped = Object.prototype.hasOwnProperty.call(input, "timestampedTranscript");

  if (!hasPlain && !hasTimestamped) {
    throw new TypeError("at least one transcript form must be supplied");
  }

  const rawTimestamped = hasTimestamped
    ? requireString(input.timestampedTranscript, "timestampedTranscript")
    : "";
  const timestampedTranscript = hasTimestamped ? normalizeTranscript(rawTimestamped) : "";

  let transcript: string;
  let derivedFrom: TranscriptDerivedFrom;
  if (hasPlain) {
    transcript = normalizeTranscript(requireString(input.transcript, "transcript"));
    derivedFrom = hasTimestamped ? "both" : "plain-only";
  } else {
    transcript = derivePlainTranscript(rawTimestamped);
    derivedFrom = "timestamped-only";
  }

  return {
    transcript,
    timestampedTranscript,
    derivedFrom,
    contentHash: computeNormalizedContentHash(transcript, timestampedTranscript),
  };
}

function concatBytes(...parts: Uint8Array[]): Uint8Array {
  const totalLength = parts.reduce((sum, part) => sum + part.length, 0);
  const result = new Uint8Array(totalLength);
  let offset = 0;
  for (const part of parts) {
    result.set(part, offset);
    offset += part.length;
  }
  return result;
}

/** Build the exact UTF-8 framed input for transcript-hash-v1. */
export function frameTranscriptHashInput(
  transcript: string,
  timestampedTranscript: string,
): Uint8Array {
  const normalizedTranscript = requireString(transcript, "transcript");
  const normalizedTimestamped = requireString(timestampedTranscript, "timestampedTranscript");
  const transcriptBytes = textEncoder.encode(normalizedTranscript);
  const timestampedBytes = textEncoder.encode(normalizedTimestamped);

  return concatBytes(
    textEncoder.encode(HASH_VERSION_PREFIX),
    textEncoder.encode(`${transcriptBytes.length}:`),
    transcriptBytes,
    textEncoder.encode(`${timestampedBytes.length}:`),
    timestampedBytes,
  );
}

/** Compute a hash after normalizing both supplied forms. */
export function computeContentHash(transcript: string, timestampedTranscript: string): string {
  return computeNormalizedContentHash(
    normalizeTranscript(transcript),
    normalizeTranscript(timestampedTranscript),
  );
}

/** Compute a hash for forms that are already normalized. */
export function computeNormalizedContentHash(
  transcript: string,
  timestampedTranscript: string,
): string {
  return sha256Hex(frameTranscriptHashInput(transcript, timestampedTranscript));
}

function rightRotate(value: number, amount: number): number {
  return (value >>> amount) | (value << (32 - amount));
}

const SHA256_K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
  0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
  0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
  0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
  0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
  0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

/** Browser-safe SHA-256 over a byte sequence. */
export function sha256Bytes(input: Uint8Array): Uint8Array {
  // The supported payloads are far below Number's precision boundary, so the
  // low 32-bit word and high 32-bit word can be written from this byte length.
  const paddedLength = Math.ceil((input.length + 9) / 64) * 64;
  const padded = new Uint8Array(paddedLength);
  padded.set(input);
  padded[input.length] = 0x80;

  const bitLength = input.length * 8;
  const highBits = Math.floor(bitLength / 0x100000000);
  const lowBits = bitLength >>> 0;
  const lengthOffset = padded.length - 8;
  padded[lengthOffset] = (highBits >>> 24) & 0xff;
  padded[lengthOffset + 1] = (highBits >>> 16) & 0xff;
  padded[lengthOffset + 2] = (highBits >>> 8) & 0xff;
  padded[lengthOffset + 3] = highBits & 0xff;
  padded[lengthOffset + 4] = (lowBits >>> 24) & 0xff;
  padded[lengthOffset + 5] = (lowBits >>> 16) & 0xff;
  padded[lengthOffset + 6] = (lowBits >>> 8) & 0xff;
  padded[lengthOffset + 7] = lowBits & 0xff;

  const hash = new Uint32Array([
    0x6a09e667,
    0xbb67ae85,
    0x3c6ef372,
    0xa54ff53a,
    0x510e527f,
    0x9b05688c,
    0x1f83d9ab,
    0x5be0cd19,
  ]);
  const schedule = new Uint32Array(64);

  for (let offset = 0; offset < padded.length; offset += 64) {
    for (let index = 0; index < 16; index += 1) {
      const byteOffset = offset + index * 4;
      schedule[index] =
        ((padded[byteOffset] << 24) |
          (padded[byteOffset + 1] << 16) |
          (padded[byteOffset + 2] << 8) |
          padded[byteOffset + 3]) >>>
        0;
    }

    for (let index = 16; index < 64; index += 1) {
      const w15 = schedule[index - 15];
      const w2 = schedule[index - 2];
      const s0 = rightRotate(w15, 7) ^ rightRotate(w15, 18) ^ (w15 >>> 3);
      const s1 = rightRotate(w2, 17) ^ rightRotate(w2, 19) ^ (w2 >>> 10);
      schedule[index] = (schedule[index - 16] + s0 + schedule[index - 7] + s1) >>> 0;
    }

    let a = hash[0];
    let b = hash[1];
    let c = hash[2];
    let d = hash[3];
    let e = hash[4];
    let f = hash[5];
    let g = hash[6];
    let h = hash[7];

    for (let index = 0; index < 64; index += 1) {
      const s1 = rightRotate(e, 6) ^ rightRotate(e, 11) ^ rightRotate(e, 25);
      const choose = (e & f) ^ (~e & g);
      const temp1 = (h + s1 + choose + SHA256_K[index] + schedule[index]) >>> 0;
      const s0 = rightRotate(a, 2) ^ rightRotate(a, 13) ^ rightRotate(a, 22);
      const majority = (a & b) ^ (a & c) ^ (b & c);
      const temp2 = (s0 + majority) >>> 0;

      h = g;
      g = f;
      f = e;
      e = (d + temp1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (temp1 + temp2) >>> 0;
    }

    hash[0] = (hash[0] + a) >>> 0;
    hash[1] = (hash[1] + b) >>> 0;
    hash[2] = (hash[2] + c) >>> 0;
    hash[3] = (hash[3] + d) >>> 0;
    hash[4] = (hash[4] + e) >>> 0;
    hash[5] = (hash[5] + f) >>> 0;
    hash[6] = (hash[6] + g) >>> 0;
    hash[7] = (hash[7] + h) >>> 0;
  }

  const result = new Uint8Array(32);
  for (let index = 0; index < hash.length; index += 1) {
    result[index * 4] = hash[index] >>> 24;
    result[index * 4 + 1] = hash[index] >>> 16;
    result[index * 4 + 2] = hash[index] >>> 8;
    result[index * 4 + 3] = hash[index];
  }
  return result;
}

export function sha256Hex(input: Uint8Array): string {
  return Array.from(sha256Bytes(input), (byte) => byte.toString(16).padStart(2, "0")).join("");
}
