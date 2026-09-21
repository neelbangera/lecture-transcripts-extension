import {
  CourseConfigError,
  getCourseMapping,
  normalizeCourseLabel,
  normalizeTerm,
  type CourseConfig,
} from "./course-config";
import {
  computeNormalizedContentHash,
  normalizeTranscript,
  normalizeTranscriptForms,
} from "./transcript-normalizer";

export const TRANSCRIPT_JOB_SCHEMA_VERSION = 1 as const;
export const MAX_TRANSCRIPT_BYTES = 450 * 1024;
export const MAX_SERIALIZED_JOB_BYTES = 950 * 1024;
export const MAX_SOURCE_URL_BYTES = 2048;
export const MAX_OTHER_STRING_CHARACTERS = 256;
export const MAX_NATIVE_MESSAGE_BYTES = 1024 * 1024;

const SOURCE_HOST = "leccap.engin.umich.edu";
const COURSE_SLUG_RE = /^[a-z0-9]+$/;
const LECTURE_KEY_RE = /^[a-z0-9]+\/\d{4}-(?:winter|spring|summer|fall)\/\d{3}$/;
const HASH_RE = /^[0-9a-f]{64}$/;
const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/;
const CAPTURED_AT_RE =
  /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})Z$/;

const TRANSCRIPT_JOB_FIELDS = [
  "schemaVersion",
  "lectureKey",
  "courseSlug",
  "courseName",
  "term",
  "lectureNumber",
  "lectureDate",
  "sourceUrl",
  "capturedAt",
  "transcript",
  "timestampedTranscript",
  "contentHash",
] as const;

export type TranscriptJobField = (typeof TRANSCRIPT_JOB_FIELDS)[number];

export interface TranscriptJob {
  readonly schemaVersion: typeof TRANSCRIPT_JOB_SCHEMA_VERSION;
  readonly lectureKey: string;
  readonly courseSlug: string;
  readonly courseName: string;
  /** Normalized YYYY-season form, for example `2026-fall`. */
  readonly term: string;
  readonly lectureNumber: number;
  readonly lectureDate: string;
  readonly sourceUrl: string;
  readonly capturedAt: string;
  readonly transcript: string;
  readonly timestampedTranscript: string;
  readonly contentHash: string;
}

export interface TranscriptJobInput {
  readonly courseName: string;
  readonly courseSlug?: string;
  readonly term: string;
  readonly lectureNumber: number;
  readonly lectureDate: string;
  readonly sourceUrl: string;
  readonly capturedAt?: string | Date;
  readonly transcript: string;
  readonly timestampedTranscript?: string;
}

export type TranscriptJobValidationCode =
  | "rejected_missing_identity"
  | "rejected_ambiguous_metadata"
  | "rejected_oversized"
  | "rejected_invalid_hash"
  | "rejected_unsafe_url"
  | "rejected_unknown_field"
  | "rejected_invalid_schema";

export interface TranscriptJobValidationErrorInfo {
  readonly code: TranscriptJobValidationCode;
  readonly field?: string;
  readonly message: string;
}

export type TranscriptJobValidationResult =
  | { readonly valid: true; readonly job: TranscriptJob }
  | { readonly valid: false; readonly error: TranscriptJobValidationErrorInfo };

export class TranscriptJobValidationError extends Error {
  readonly code: TranscriptJobValidationCode;
  readonly field?: string;

  constructor(error: TranscriptJobValidationErrorInfo) {
    super(error.message);
    this.name = "TranscriptJobValidationError";
    this.code = error.code;
    this.field = error.field;
  }
}

export type SourceUrlErrorCode =
  | "invalid_absolute_url"
  | "unsafe_scheme"
  | "unsafe_host"
  | "userinfo_not_allowed"
  | "port_not_allowed"
  | "too_long";

export class SourceUrlError extends Error {
  readonly code: SourceUrlErrorCode;

  constructor(code: SourceUrlErrorCode, message: string) {
    super(message);
    this.name = "SourceUrlError";
    this.code = code;
  }
}

export interface SourceUrlInfo {
  readonly canonicalUrl: string;
  readonly publishUrl: string | null;
  readonly logValue: string;
}

const textEncoder = new TextEncoder();

function utf8ByteLength(value: string): number {
  return textEncoder.encode(value).length;
}

function characterLength(value: string): number {
  return Array.from(value).length;
}

function isWholeCalendarDate(year: number, month: number, day: number): boolean {
  if (year < 1 || month < 1 || month > 12 || day < 1) {
    return false;
  }

  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const daysInMonth = [31, leapYear ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return day <= daysInMonth[month - 1];
}

function isValidDate(value: string): boolean {
  const match = DATE_RE.exec(value);
  return Boolean(
    match && isWholeCalendarDate(Number(match[1]), Number(match[2]), Number(match[3])),
  );
}

function isValidCapturedAt(value: string): boolean {
  const match = CAPTURED_AT_RE.exec(value);
  return Boolean(
    match &&
      isWholeCalendarDate(Number(match[1]), Number(match[2]), Number(match[3])) &&
      Number(match[4]) <= 23 &&
      Number(match[5]) <= 59 &&
      Number(match[6]) <= 59,
  );
}

function validationFailure(
  code: TranscriptJobValidationCode,
  message: string,
  field?: string,
): TranscriptJobValidationResult {
  return { valid: false, error: { code, field, message } };
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasOwn(value: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isKnownJobField(key: string): key is TranscriptJobField {
  return (TRANSCRIPT_JOB_FIELDS as readonly string[]).includes(key);
}

function canonicalJobObject(job: TranscriptJob): TranscriptJob {
  // Reconstruct in schema order so JSON serialization is deterministic even
  // when a caller supplied an object with a different insertion order.
  return {
    schemaVersion: job.schemaVersion,
    lectureKey: job.lectureKey,
    courseSlug: job.courseSlug,
    courseName: job.courseName,
    term: job.term,
    lectureNumber: job.lectureNumber,
    lectureDate: job.lectureDate,
    sourceUrl: job.sourceUrl,
    capturedAt: job.capturedAt,
    transcript: job.transcript,
    timestampedTranscript: job.timestampedTranscript,
    contentHash: job.contentHash,
  };
}

function toError(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}

/**
 * Canonicalize a source URL for transport/storage.  URL parsing may normalize
 * URL syntax, but the encoded pathname itself is never decoded or rewritten.
 */
export function canonicalizeSourceUrl(sourceUrl: string): string {
  if (typeof sourceUrl !== "string") {
    throw new SourceUrlError("invalid_absolute_url", "sourceUrl must be a string");
  }

  let parsed: URL;
  try {
    parsed = new URL(sourceUrl);
  } catch {
    throw new SourceUrlError("invalid_absolute_url", "sourceUrl must be an absolute URL");
  }

  if (parsed.protocol.toLowerCase() !== "https:") {
    throw new SourceUrlError("unsafe_scheme", "sourceUrl must use https");
  }
  if (parsed.hostname.toLowerCase() !== SOURCE_HOST) {
    throw new SourceUrlError("unsafe_host", `sourceUrl host must be ${SOURCE_HOST}`);
  }
  if (parsed.username || parsed.password) {
    throw new SourceUrlError("userinfo_not_allowed", "sourceUrl userinfo is not allowed");
  }
  // WHATWG URL normalizes an explicit default HTTPS port to the empty string;
  // every remaining non-empty port is non-default and therefore rejected.
  if (parsed.port) {
    throw new SourceUrlError("port_not_allowed", "sourceUrl may not use a non-default port");
  }

  const path = parsed.pathname || "/";
  const canonicalUrl = `https://${SOURCE_HOST}${path}`;
  if (utf8ByteLength(canonicalUrl) > MAX_SOURCE_URL_BYTES) {
    throw new SourceUrlError("too_long", "canonical sourceUrl exceeds 2048 UTF-8 bytes");
  }

  return canonicalUrl;
}

/**
 * Return the canonical URL only when it is safe to publish in Markdown.  A
 * transport-valid URL containing a whole sensitive path token is retained in
 * the queue but omitted from published frontmatter.
 */
export function sanitizeSourceUrlForPublish(sourceUrl: string): string | null {
  const canonicalUrl = canonicalizeSourceUrl(sourceUrl);
  const path = new URL(canonicalUrl).pathname.toLowerCase();
  const sensitiveSegments = new Set(["token", "session", "auth", "sid"]);
  const hasSensitiveSegment = path.split(/[/._-]/).some((segment) => sensitiveSegments.has(segment));
  return hasSensitiveSegment ? null : canonicalUrl;
}

export function sourceUrlInfo(sourceUrl: string): SourceUrlInfo {
  const canonicalUrl = canonicalizeSourceUrl(sourceUrl);
  const parsed = new URL(canonicalUrl);
  return {
    canonicalUrl,
    publishUrl: sanitizeSourceUrlForPublish(canonicalUrl),
    logValue: `${parsed.host}${parsed.pathname}`,
  };
}

function formatCapturedAt(value: string | Date | undefined): string {
  const date = value === undefined ? new Date() : value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    throw new TranscriptJobValidationError({
      code: "rejected_invalid_schema",
      field: "capturedAt",
      message: "capturedAt must be a valid timestamp",
    });
  }
  return date.toISOString().replace(/\.\d{3}Z$/, "Z");
}

function getMappingForJob(job: Record<string, unknown>): CourseConfig | TranscriptJobValidationResult {
  try {
    const mapping = getCourseMapping(job.courseName as string, job.term as string);
    if (mapping.courseName !== job.courseName) {
      return validationFailure(
        "rejected_invalid_schema",
        "courseName must be the canonical Stage 0 course name",
        "courseName",
      );
    }
    if (mapping.courseSlug !== job.courseSlug) {
      return validationFailure(
        "rejected_invalid_schema",
        "courseSlug does not match the Stage 0 course mapping",
        "courseSlug",
      );
    }
    if (normalizeTerm(job.term as string) !== job.term) {
      return validationFailure(
        "rejected_invalid_schema",
        "term must use normalized YYYY-season form",
        "term",
      );
    }
    return mapping;
  } catch (error) {
    const message = error instanceof CourseConfigError ? error.message : toError(error);
    return validationFailure("rejected_invalid_schema", message, "courseName");
  }
}

function nonWhitespaceCharacterCount(value: string): number {
  return Array.from(value).filter((character) => !/\s/u.test(character)).length;
}

function isExactLoadingMarker(value: string): boolean {
  return /^(?:loading…|loading transcript|no transcript)$/i.test(value);
}

/** Validate an externally received, schema-shaped job without mutating it. */
export function validateTranscriptJob(value: unknown): TranscriptJobValidationResult {
  if (!isObject(value)) {
    return validationFailure("rejected_invalid_schema", "TranscriptJob must be a JSON object");
  }

  for (const key of Object.keys(value)) {
    if (!isKnownJobField(key)) {
      return validationFailure(
        "rejected_unknown_field",
        `unknown TranscriptJob field: ${key}`,
        key,
      );
    }
  }
  for (const field of TRANSCRIPT_JOB_FIELDS) {
    if (!hasOwn(value, field)) {
      return validationFailure(
        "rejected_invalid_schema",
        `missing TranscriptJob field: ${field}`,
        field,
      );
    }
  }

  if (value.schemaVersion !== TRANSCRIPT_JOB_SCHEMA_VERSION) {
    return validationFailure(
      "rejected_invalid_schema",
      "schemaVersion must equal 1",
      "schemaVersion",
    );
  }

  const stringFields: readonly TranscriptJobField[] = [
    "lectureKey",
    "courseSlug",
    "courseName",
    "term",
    "lectureDate",
    "sourceUrl",
    "capturedAt",
    "transcript",
    "timestampedTranscript",
    "contentHash",
  ];
  for (const field of stringFields) {
    if (typeof value[field] !== "string") {
      return validationFailure(
        "rejected_invalid_schema",
        `${field} must be a string`,
        field,
      );
    }
  }
  if (typeof value.lectureNumber !== "number" || !Number.isInteger(value.lectureNumber)) {
    return validationFailure(
      "rejected_invalid_schema",
      "lectureNumber must be an integer",
      "lectureNumber",
    );
  }
  if (value.lectureNumber < 1 || value.lectureNumber > 999) {
    return validationFailure(
      "rejected_invalid_schema",
      "lectureNumber must be between 1 and 999",
      "lectureNumber",
    );
  }

  for (const field of [
    "lectureKey",
    "courseSlug",
    "courseName",
    "term",
    "lectureDate",
    "capturedAt",
    "contentHash",
  ] as const) {
    const fieldValue = value[field] as string;
    if (characterLength(fieldValue) > MAX_OTHER_STRING_CHARACTERS) {
      return validationFailure(
        "rejected_oversized",
        `${field} exceeds ${MAX_OTHER_STRING_CHARACTERS} characters`,
        field,
      );
    }
  }

  const sourceUrl = value.sourceUrl as string;
  let canonicalSourceUrl: string;
  try {
    canonicalSourceUrl = canonicalizeSourceUrl(sourceUrl);
  } catch (error) {
    return validationFailure("rejected_unsafe_url", toError(error), "sourceUrl");
  }
  if (sourceUrl !== canonicalSourceUrl) {
    return validationFailure(
      "rejected_unsafe_url",
      "sourceUrl must already be canonicalized",
      "sourceUrl",
    );
  }

  const mappingResult = getMappingForJob(value);
  if (!isCourseConfig(mappingResult)) {
    return mappingResult;
  }

  if (!LECTURE_KEY_RE.test(value.lectureKey as string)) {
    return validationFailure(
      "rejected_invalid_schema",
      "lectureKey has an invalid shape",
      "lectureKey",
    );
  }
  const expectedLectureKey = deriveLectureKey(
    value.courseSlug as string,
    value.term as string,
    value.lectureNumber as number,
  );
  if (value.lectureKey !== expectedLectureKey) {
    return validationFailure(
      "rejected_invalid_schema",
      "lectureKey is not derived from courseSlug, term, and lectureNumber",
      "lectureKey",
    );
  }

  if (!isValidDate(value.lectureDate as string)) {
    return validationFailure(
      "rejected_invalid_schema",
      "lectureDate must be a valid YYYY-MM-DD date",
      "lectureDate",
    );
  }
  if (!isValidCapturedAt(value.capturedAt as string)) {
    return validationFailure(
      "rejected_invalid_schema",
      "capturedAt must be RFC3339 UTC with whole-second precision",
      "capturedAt",
    );
  }

  const transcript = value.transcript as string;
  const timestampedTranscript = value.timestampedTranscript as string;
  const normalizedTranscript = normalizeTranscript(transcript);
  const normalizedTimestampedTranscript = normalizeTranscript(timestampedTranscript);
  if (transcript !== normalizedTranscript) {
    return validationFailure(
      "rejected_invalid_schema",
      "transcript must be normalized before submission",
      "transcript",
    );
  }
  if (timestampedTranscript !== normalizedTimestampedTranscript) {
    return validationFailure(
      "rejected_invalid_schema",
      "timestampedTranscript must be normalized before submission",
      "timestampedTranscript",
    );
  }
  if (utf8ByteLength(transcript) > MAX_TRANSCRIPT_BYTES) {
    return validationFailure(
      "rejected_oversized",
      `transcript exceeds ${MAX_TRANSCRIPT_BYTES} UTF-8 bytes`,
      "transcript",
    );
  }
  if (utf8ByteLength(timestampedTranscript) > MAX_TRANSCRIPT_BYTES) {
    return validationFailure(
      "rejected_oversized",
      `timestampedTranscript exceeds ${MAX_TRANSCRIPT_BYTES} UTF-8 bytes`,
      "timestampedTranscript",
    );
  }
  if (nonWhitespaceCharacterCount(transcript) < 50) {
    return validationFailure(
      "rejected_invalid_schema",
      "transcript must contain at least 50 non-whitespace characters",
      "transcript",
    );
  }
  if (isExactLoadingMarker(transcript.trim())) {
    return validationFailure(
      "rejected_invalid_schema",
      "transcript is an exact loading/empty marker",
      "transcript",
    );
  }

  const contentHash = value.contentHash as string;
  if (!HASH_RE.test(contentHash)) {
    return validationFailure(
      "rejected_invalid_hash",
      "contentHash must be 64 lowercase hexadecimal characters",
      "contentHash",
    );
  }
  const expectedHash = computeNormalizedContentHash(transcript, timestampedTranscript);
  if (contentHash !== expectedHash) {
    return validationFailure(
      "rejected_invalid_hash",
      "contentHash does not match the framed normalized transcript forms",
      "contentHash",
    );
  }

  const canonicalJob = canonicalJobObject(value as unknown as TranscriptJob);
  if (utf8ByteLength(JSON.stringify(canonicalJob)) > MAX_SERIALIZED_JOB_BYTES) {
    return validationFailure(
      "rejected_oversized",
      `serialized TranscriptJob exceeds ${MAX_SERIALIZED_JOB_BYTES} UTF-8 bytes`,
    );
  }

  return { valid: true, job: canonicalJob };
}

function isCourseConfig(value: CourseConfig | TranscriptJobValidationResult): value is CourseConfig {
  return !("valid" in value);
}

/** Throw a typed error when a job is not valid. */
export function assertValidTranscriptJob(value: unknown): TranscriptJob {
  const result = validateTranscriptJob(value);
  if (!result.valid) {
    throw new TranscriptJobValidationError(result.error);
  }
  return result.job;
}

/** Derive the deterministic lecture identity used for deduplication. */
export function deriveLectureKey(
  courseSlug: string,
  term: string,
  lectureNumber: number,
): string {
  if (typeof courseSlug !== "string" || !COURSE_SLUG_RE.test(courseSlug)) {
    throw new TranscriptJobValidationError({
      code: "rejected_invalid_schema",
      field: "courseSlug",
      message: "courseSlug must contain lowercase ASCII alphanumeric characters only",
    });
  }
  const normalizedTerm = normalizeTerm(term);
  if (!Number.isInteger(lectureNumber) || lectureNumber < 1 || lectureNumber > 999) {
    throw new TranscriptJobValidationError({
      code: "rejected_invalid_schema",
      field: "lectureNumber",
      message: "lectureNumber must be an integer between 1 and 999",
    });
  }
  return `${courseSlug}/${normalizedTerm}/${String(lectureNumber).padStart(3, "0")}`;
}

/** Derive the write-once Markdown path for a validated lecture identity. */
export function deriveStableLecturePath(
  courseSlug: string,
  term: string,
  lectureNumber: number,
): string {
  const lectureKey = deriveLectureKey(courseSlug, term, lectureNumber);
  return `courses/${lectureKey.split("/")[0]}/${lectureKey.split("/")[1]}/lectures/${lectureKey.split("/")[2]}.md`;
}

/** Build, normalize, hash, canonicalize, and validate a wire-shaped job. */
export function createTranscriptJob(input: TranscriptJobInput): TranscriptJob {
  let mapping: CourseConfig;
  try {
    mapping = getCourseMapping(input.courseName, input.term);
  } catch (error) {
    throw new TranscriptJobValidationError({
      code: "rejected_invalid_schema",
      field: "courseName",
      message: toError(error),
    });
  }

  if (input.courseSlug !== undefined && input.courseSlug !== mapping.courseSlug) {
    throw new TranscriptJobValidationError({
      code: "rejected_invalid_schema",
      field: "courseSlug",
      message: "courseSlug does not match the Stage 0 course mapping",
    });
  }

  const normalizedTerm = normalizeTerm(input.term);
  const forms =
    input.timestampedTranscript === undefined
      ? normalizeTranscriptForms({ transcript: input.transcript })
      : normalizeTranscriptForms({
          transcript: input.transcript,
          timestampedTranscript: input.timestampedTranscript,
        });

  let sourceUrl: string;
  try {
    sourceUrl = canonicalizeSourceUrl(input.sourceUrl);
  } catch (error) {
    throw new TranscriptJobValidationError({
      code: "rejected_unsafe_url",
      field: "sourceUrl",
      message: toError(error),
    });
  }

  const job: TranscriptJob = {
    schemaVersion: TRANSCRIPT_JOB_SCHEMA_VERSION,
    lectureKey: deriveLectureKey(mapping.courseSlug, normalizedTerm, input.lectureNumber),
    courseSlug: mapping.courseSlug,
    courseName: mapping.courseName,
    term: normalizedTerm,
    lectureNumber: input.lectureNumber,
    lectureDate: input.lectureDate,
    sourceUrl,
    capturedAt: formatCapturedAt(input.capturedAt),
    transcript: forms.transcript,
    timestampedTranscript: forms.timestampedTranscript,
    contentHash: forms.contentHash,
  };

  return assertValidTranscriptJob(job);
}

/** Alias kept explicit for callers that prefer a builder-style name. */
export const buildTranscriptJob = createTranscriptJob;

export function serializeTranscriptJob(job: unknown): string {
  return JSON.stringify(assertValidTranscriptJob(job));
}

export function transcriptJobByteLength(job: unknown): number {
  return utf8ByteLength(serializeTranscriptJob(job));
}

// Kept as a named export for parser/content implementations that want the
// canonical course label without reaching into the config module.
export { normalizeCourseLabel };
