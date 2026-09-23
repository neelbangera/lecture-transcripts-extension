# internal/protocol Architecture

## Purpose

`internal/protocol` is the Go half of the closed Native Messaging contract: it
defines the wire types, the canonical serializer, and every decoder/validator
that decides whether an extension message is allowed to reach the queue or
whether an uploader response is allowed to reach Chrome. It is the defensive
counterpart to the TypeScript client and to `protocol/*.schema.json`.

- Files: `job.go` (job type + canonical serializer), `messages.go` (request,
  acknowledgement, command, status, auth, queue, and error vocabularies),
  `validate.go` (strict decoding and validation), plus the cross-language
  fixture under `testdata/`.
- Authority: `TECHNICAL_PLAN.md` lines 287–450 and 496–642, rendered
  machine-readably in `/protocol/*.schema.json` and
  `/protocol/*-vectors.json`.

## Boundaries and dependencies

- Imports only the standard library (`bytes`, `encoding/json`, `errors`, `io`,
  `net/url`, `regexp`, `strings`, `time`, `unicode`, `unicode/utf8`). No
  internal package, no third-party module.
- Imported by `host`, `queue`, `auth`, `github`, `markdown`, `processor`, and
  `logging`; it imports none of them, so it can never grow a dependency cycle.
- The package is the only place transcript text is typed (`TranscriptJob`).
  Every response/status type is transcript-free by construction.
- `protocol` does not normalize transcript text (NFC/whitespace); it validates
  UTF-8 and sizes and trusts the extension's normalization. See the open
  question in `uploader/ARCHITECTURE.md`.

## Contracts and invariants

### Limits and vocabulary constants

```go
ProtocolVersion      = 1
MaxFrameBytes        = 1 << 20  // 1,048,576 bytes excluding the 4-byte prefix
MaxSerializedJobByte = 972800   // 950 KiB, compact canonical job JSON
MaxTranscriptBytes   = 460800   // 450 KiB per transcript representation
MaxSourceURLBytes    = 2048     // canonical source URL, UTF-8 bytes
KindLecture          = "lecture"
KindDiscussion       = "discussion"
```

`config` re-declares the application-facing limit names and must stay equal
(`config.TestLimitsMatchProtocolContract`). Queue statuses, ack statuses,
duplicate actions, auth states, drain states, remote file kinds, and error
categories are closed enums defined in `messages.go` and mirrored in
`protocol/native-messaging.schema.json` (`schema_test.go` compares them).

### TranscriptJob (wire type and field order)

`TranscriptJob` is the only value allowed to carry transcript text. Field order
is canonical and preserved by `encoding/json`; the JSON property order is the
schema order and is used for byte measurements and cross-language fixtures:

| # | Go field | JSON key | Type / limits | Validation (category on failure) |
| --- | --- | --- | --- | --- |
| 1 | `SchemaVersion` | `schemaVersion` | integer | must equal `1` (`rejected_invalid_schema`) |
| 2 | `Kind` | `kind` | `"lecture"` or `"discussion"` | enum (`rejected_invalid_schema`) |
| 3 | `LectureKey` | `lectureKey` | `^[a-z0-9]+/[0-9]{4}-(winter\|spring\|summer\|fall)/[0-9]{3}$`, ≤128 runes | pattern + length; must equal `courseSlug + "/" + term + "/" + zeroPad(lectureNumber)` |
| 4 | `CourseSlug` | `courseSlug` | `^[a-z0-9]+$`, ≤64 runes | pattern + length |
| 5 | `CourseName` | `courseName` | nonempty, ≤256 runes, valid UTF-8, no CR/LF | `rejected_invalid_schema` |
| 6 | `Term` | `term` | `^[0-9]{4}-(winter\|spring\|summer\|fall)$`, ≤32 runes | pattern + length |
| 7 | `LectureNumber` | `lectureNumber` | integer `1..999` | range; also drives `zeroPadLecture` for the key check |
| 8 | `LectureDate` | `lectureDate` | `YYYY-MM-DD` | must parse as a real calendar date (`time.Parse("2006-01-02")` round-trips) |
| 9 | `SourceURL` | `sourceUrl` | canonical URL ≤2048 UTF-8 bytes | `CanonicalizeSourceURL` must succeed (`rejected_unsafe_url`) |
| 10 | `CapturedAt` | `capturedAt` | `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$` | parses to the identical whole-second UTC string |
| 11 | `Transcript` | `transcript` | valid UTF-8, ≤460800 bytes, ≥50 non-whitespace runes | invalid UTF-8/short → `rejected_invalid_schema`; over byte cap → `rejected_oversized` |
| 12 | `TimestampedTranscript` | `timestampedTranscript` | valid UTF-8, ≤460800 bytes, may be empty | invalid UTF-8 → `rejected_invalid_schema`; over cap → `rejected_oversized` |
| 13 | `ContentHash` | `contentHash` | `^[0-9a-f]{64}$` | `rejected_invalid_hash` |

After field validation, `ValidateJob` serializes with `MarshalCanonical` and
rejects a result over `MaxSerializedJobByte` as `rejected_oversized`. Checks run
in source order (the table order above, with the `lectureKey` consistency check
evaluated after `term` and before the `lectureNumber` range check), so the first
failing rule determines the category. `CanonicalizeJob` runs
`CanonicalizeSourceURL` on `SourceURL`, then re-validates the whole job;
`queue.Enqueue` persists only the result.

### Source URL canonicalization

`CanonicalizeSourceURL(raw)`:

- rejects empty, CR/LF-containing, or non-UTF-8 input;
- `url.Parse` must succeed with scheme and host, no userinfo, no opaque part;
- scheme must be `https` (case-insensitive); hostname must be
  `leccap.engin.umich.edu` (case-insensitive); port must be absent or `443`;
- query and fragment are dropped; `EscapedPath()` is preserved (empty path
  becomes `/`, and the path must start with `/`);
- result is `"https://leccap.engin.umich.edu" + EscapedPath()` and must be
  ≤2048 bytes; otherwise the job fails as `rejected_unsafe_url`.

Sensitive path segments (`token`, `session`, `auth`, `sid`) do **not** make the
URL invalid here; `markdown.PublishSourceURL` decides whether the canonical URL
is omitted from published metadata. `protocol/source-url-vectors.json` is the
shared expectation file (`TestCanonicalizeSourceURLVectors`).

### Requests

`DecodeRequest(data)` is the authoritative parser; callers must not decode
frames directly. It first enforces `len(data) <= MaxFrameBytes`
(`rejected_oversized`) and `strictJSONObject`: valid UTF-8, exactly one JSON
object, no trailing values, and no duplicate keys at any depth. It then reads
the envelope and per-type fields:

| Request | Required keys | Optional keys | Rules |
| --- | --- | --- | --- |
| `connect` | `type`, `protocolVersion`, `requestId`, `extensionVersion` | — | `extensionVersion` is `[\x20-\x7e]{1,32}` |
| `submit_job` | `type`, `protocolVersion`, `requestId`, `job` | — | `job` decoded by `decodeJob` |
| `status_request` | `type`, `protocolVersion`, `requestId` | `beforeJobId`, `limit` | `beforeJobId` positive int64; `limit` `1..50`; `EffectiveLimit()` defaults to 50 |
| `retry_job` | `type`, `protocolVersion`, `requestId`, `jobId` | — | `jobId` positive int64 |
| `discard_job` | `type`, `protocolVersion`, `requestId`, `jobId`, `confirmation` | — | `jobId` positive int64; `confirmation` must equal `"discard"` |
| `reset` | `type`, `protocolVersion`, `requestId` | — | no other keys |

Envelope rules:

- `requestId` matches `^[\x20-\x7e]{1,64}$` (printable ASCII, 1–64).
- `protocolVersion` must be present and integer; any value other than `1`
  returns `ErrorProtocolMismatch`.
- Unknown keys return `ErrorRejectedUnknownField`; missing required keys,
  `null`, wrong JSON types, floats where integers are required, or an unknown
  `type` return `ErrorRejectedInvalidSchema`.
- `DecodeRequest` returns a **partially populated** `Request` alongside the
  error so a `submit_job` rejection can still echo `requestId` and any valid
  `lectureKey`/`contentHash` (`host.rejectedAck`,
  `processor.rejectedSubmitAck`).

### Responses

`EncodeResponse(message any)` validates with `ValidateResponse` and only then
`json.Marshal`s; the host never frames an unvalidated value. `DecodeResponse`
is the strict test-side inverse: it requires every schema-required key at the
top level and inside `authorization`, `counts`, and each `jobs[]` entry,
rejects unknown fields, and runs the same validators.

**Ack** (`submit_job` only): `type:"ack"`, `protocolVersion:1`,
`requestId` (pattern), `operation:"submit"`, `jobId` null or positive,
`lectureKey` null or valid lecture key, `contentHash` null or 64-hex,
`status` in `queued`, `already_queued`, `rejected_invalid_schema`,
`rejected_unknown_field`, `rejected_oversized`, `rejected_invalid_hash`,
`rejected_unsafe_url`, `rejected_queue_full`, `rejected_duplicate_terminal`;
`existingStatus` null or a queue status; `action` null or `retry_existing` /
`discard_existing_then_recapture`. `rejected_duplicate_terminal` is ack-only
and never appears in an `error` message.

**CommandResult** (`retry_job`, `discard_job`, `reset`): `operation` in
`retry`/`discard`/`reset`; `result` in `accepted`/`rejected`; `jobId` null or
positive; `status` null, a queue status, or the literals `discarded`/`reset`;
`errorCategory` null or a known category. Accepted retry is
`accepted/queued`, ineligible retry is `rejected` with the current status and
`errorCategory=ineligible_command`; accepted discard is `accepted/discarded`;
accepted reset is `accepted/reset`.

**Status**: `extensionVersion` and `uploaderVersion` are
`[\x20-\x7e]{1,32}`; `requestId` is null or a valid request ID (unsolicited
status uses null); `authState` and `drainState` are the enums below; `counts`
has exactly the 15 nonnegative queue-status keys; `jobs` must be non-null and
at most 50 validated `JobSummary` entries; `nextBeforeJobId` is null or
positive. `JobSummary` is exactly `{jobId, lectureKey, contentHash, status,
attemptCount, nextAttemptAt, updatedAt, targetPath, lastErrorCategory,
lastErrorHttpStatus, remoteContentHash, remoteFileKind}`: `jobId > 0`;
`lectureKey` pattern, ≤128; `contentHash` 64-hex; `status` a queue status;
`attemptCount >= 0`; timestamps whole-second RFC3339 UTC or null;
`targetPath` nonempty, ≤512 runes; `lastErrorCategory` known and ≤64 runes or
null; `lastErrorHttpStatus` 100–599 or null; `remoteContentHash` 64-hex or
null; `remoteFileKind` one of the remote enum or null. No transcript, course
prose, full URL, or free-form error text is representable.

**Error**: `type:"error"`, `protocolVersion:1`, `requestId` null or valid,
`category` a known error category, `retryable` boolean. `ProtocolError`
implements `Error()` as the bare category string, so accidental printing
cannot leak payload data.

### Queue statuses and duplicate-terminal actions

Persisted queue statuses (15, `messages.go`; counts keys are exactly these):

`queued`, `uploading`, `uploaded`, `unchanged`, `retryable_error`,
`permanent_conflict`, `rejected_missing_identity`,
`rejected_ambiguous_metadata`, `rejected_oversized`, `rejected_queue_full`,
`rejected_invalid_hash`, `rejected_unsafe_url`, `rejected_unknown_field`,
`rejected_invalid_schema`, `rejected_permission`.

`rejected_handoff_full` exists only in the error-category vocabulary
(extension-local) and is never a queue row; `rejected_duplicate_terminal` is
ack-only. `reauthorization_required`, `target_repository_unavailable`, and
`protocol_mismatch` are auth states, not rows.

Duplicate-terminal handling lives in `queue.duplicateResult` and is surfaced
through the ack:

| Existing terminal status | `status` | `existingStatus` | `action` |
| --- | --- | --- | --- |
| `rejected_permission` | `rejected_duplicate_terminal` | that status | `retry_existing` |
| any other `rejected_*` | `rejected_duplicate_terminal` | that status | `discard_existing_then_recapture` |

A duplicate of a non-terminal row is `already_queued` with `existingStatus`
set and no action; a new capture never silently replaces a terminal row.

### Status pagination

- Request: optional `beforeJobId` (positive int64 cursor) and `limit`
  (default 50, max 50).
- Store: `WHERE id < beforeJobId` when a cursor is present,
  `ORDER BY id DESC`, fetch `limit + 1` rows; if more than `limit` rows exist,
  truncate to `limit` and set `nextBeforeJobId` to the last included `jobId`,
  otherwise `null`.
- Response: `jobs` is newest-first and at most 50 entries; the next page is
  requested with `beforeJobId = nextBeforeJobId`; `null` means no more rows.
  This keeps status frames well below the 1 MiB cap even for a 500-row queue.

### Auth fields

`AuthState` values: `not_connected`, `authorizing`, `connected`,
`reauthorization_required`, `target_repository_unavailable`,
`protocol_mismatch`. `auth.Manager` sets the first five; `protocol_mismatch`
is the wire vocabulary for a version incompatibility (the decoder rejects a
non-1 `protocolVersion` as `ErrorProtocolMismatch` rather than emitting that
auth state). `authorization` carries nullable display fields only:
`userCode` printable ASCII ≤64; `verificationUri` and
`verificationUriComplete` HTTPS `github.com` URLs without userinfo, ≤2048
bytes; `expiresAt` whole-second RFC3339 UTC. `processor.authorization()`
re-validates these before emitting, so a value the validator would reject (for
example a `?user_code=` query on `verificationUriComplete`) is omitted and the
popup falls back to `verificationUri` + `userCode`.

### Canonical serialization and cross-language fixtures

`MarshalCanonical()` emits compact UTF-8 JSON with HTML escaping disabled
(`SetEscapeHTML(false)`, matching `JSON.stringify`) and **no trailing
newline**; keys keep the struct/schema order (no sorting). This byte sequence
is the size-measurement authority and the queue's stored `job_json`.

Cross-language fixture rules:

- `testdata/transcript-job.canonical.json` is produced by the TypeScript
  client and committed verbatim. `TestCanonicalJobFixtureFromTypeScript`
  asserts Go's `MarshalCanonical` output is byte-equal, the fixture has no
  trailing newline, it exercises raw `&`/`<` escaping, it is ≤
  `MaxSerializedJobByte`, it decodes/validates, and it round-trips inside a
  `submit_job` envelope.
- `TestCanonicalJobFixtureHashFraming` re-derives the fixture hash with the
  normative framing: SHA-256 over
  `ASCII("transcript-hash-v1\0")` + decimal UTF-8 byte length + `:` +
  transcript bytes + decimal length + `:` + timestamped-transcript bytes.
- `schema_test.go` reads `/protocol/native-messaging.schema.json` and
  `/protocol/transcript-job.schema.json` and asserts the Go enums and struct
  keys match; `TestCanonicalizeSourceURLVectors` reads
  `/protocol/source-url-vectors.json`.
- `protocol/normalization-vectors.json` is currently consumed only by
  `extension-tests/protocol-vectors.test.ts`; there is no Go normalizer to test
  it (see the open question in `uploader/ARCHITECTURE.md`).

## Data flow

```text
bytes from host.ReadFrame
  → DecodeRequest: size cap → strictJSONObject (UTF-8, single object, no
    duplicate keys) → envelope (type/protocolVersion/requestId) → per-type
    fields → decodeJob → ValidateJob
  → processor (only validated Requests)
  → response values (Ack | CommandResult | StatusMessage | ErrorMessage)
  → EncodeResponse: ValidateResponse → json.Marshal
  → host.WriteFrame
```

## File responsibilities

| File | Role | Key exports |
| --- | --- | --- |
| `job.go` | `TranscriptJob` type, limits, kinds, canonical serializer | `ProtocolVersion`, `MaxFrameBytes`, `MaxSerializedJobByte`, `MaxTranscriptBytes`, `MaxSourceURLBytes`, `KindLecture`, `KindDiscussion`, `TranscriptJob`, `(TranscriptJob).MarshalCanonical` |
| `messages.go` | request/response types and closed vocabularies; the only response encoder | `RequestType` + 6 constants, `QueueStatus` + 15 constants, `AckStatus` + 9 constants, `DuplicateAction` + 2 constants, `AuthState` + 6 constants, `DrainState` + 4 constants, `RemoteFileKind` + 6 constants, `ErrorCategory` + 19 constants, `ProtocolError`, `NewProtocolError`, `Request` (+`EffectiveLimit`, `MarshalJSON`), `Ack`, `CommandResult`, `Authorization`, `QueueCounts`, `JobSummary`, `StatusMessage`, `ErrorMessage`, `EncodeResponse` |
| `validate.go` | strict decode/validate for jobs, requests, and responses; enums and helpers | `DecodeRequest`, `ValidateJob`, `CanonicalizeJob`, `CanonicalizeSourceURL`, `DecodeResponse`, `ValidateResponse`, `IsQueueStatus`, `IsErrorCategory`, `IsAuthState`, `IsDrainState`, `IsRemoteFileKind`, `IsValidLectureKey`, `IsValidContentHash` |
| `testdata/transcript-job.canonical.json` | TypeScript-produced canonical job fixture (hash framing + byte parity) | — |
| `job_test.go` | job validation table, byte boundaries, URL vectors, canonical bytes, fixture parity, helpers | `TestValidateJob`, `TestValidateJobByteBoundaries`, `TestCanonicalizeSourceURLVectors`, `TestMarshalCanonical`, `TestCanonicalJobFixtureFromTypeScript`, `TestCanonicalJobFixtureHashFraming` |
| `request_test.go` | every request type and malformed envelope case | `TestDecodeRequest*`, `TestRequestMarshalJSONRoundTrip`, `TestEffectiveLimitDefaultsToFifty` |
| `response_test.go` | response round-trips, required keys, nullables, category-only errors | `TestEncodeDecodeResponseRoundTrips`, `TestEncodeResponseRejectsInvalidMessages`, `TestDecodeResponse*`, `TestResponseErrorTextIsCategoryOnly` |
| `schema_test.go` | agreement between Go and `/protocol/*.schema.json` | `TestSchemaVocabularyMatchesGo`, `TestSchemaRequestAndResponseTypes`, `TestTranscriptJobSchemaMatchesGoStruct` |
| `helpers_test.go` | shared fixtures and assertion helpers (not a production file) | `validTestJob`, `assertCategory`, `runRequestCases` |
| `IMPLEMENTATION.md` | work items and done criteria for this package | — |
| `ARCHITECTURE.md` | this document | — |

## Testing and verification

`go test ./internal/protocol` runs entirely offline and covers:

- every request type with required/optional field combinations, unknown
  fields, missing fields, duplicate keys, trailing JSON, wrong numeric types,
  invalid UTF-8, and oversized messages;
- lecture-key consistency, course/term/lecture bounds, calendar dates, UTC
  timestamps, minimum transcript content, hash format, and serialized-job
  limits (`TestValidateJob`, `TestValidateJobByteBoundaries`);
- source URL canonicalization against `/protocol/source-url-vectors.json` and
  category-only error text (`TestValidateJobErrorIsCategoryOnly`);
- response validation for acknowledgements, command results, status pages,
  auth fields, counts, job summaries, and error messages, including required
  nested keys and nullable fields;
- the cross-language fixture byte equality and hash framing, and schema
  vocabulary/struct agreement with the JSON schemas.

## Related plan sections

- Transcript job and state model — `TECHNICAL_PLAN.md` lines 147–273
  (identity, paths, hash framing, retry/terminal semantics).
- Canonical Native Messaging contract — lines 287–390 (envelopes, ack,
  command_result, status, error, pagination, drain lifecycle).
- Canonical transcript-job schema — lines 392–436.
- Canonical source-URL vectors — lines 438–450.
- Canonical status vocabulary — lines 496–537.
- Canonical normalization-vector contract — lines 539–642 (currently
  TypeScript-only; see open question).
- Stage 4, item 2 — lines 883–903 (rejection categories and limits).
- Native uploader file inventory — lines 1094–1096, 1116.

## How to change this directory safely

1. Treat `TECHNICAL_PLAN.md` and `/protocol/*.schema.json` as the contract.
   Changing a field name, enum value, limit, or category requires updating the
   plan, the schemas, the TypeScript client, and the fixture together.
2. Keep the package dependency-free. If validation needs a new external
   library, it is almost certainly the wrong layer.
3. Never weaken strictness: no unknown fields, no duplicate keys, no trailing
   JSON, no implicit defaults for missing keys (the one documented default is
   `status_request.limit`, applied by `EffectiveLimit`).
4. Keep errors category-only. Never wrap raw JSON, transcript text, URLs, or
   remote bodies into a `ProtocolError` or a response.
5. Update the canonical fixture whenever the job shape or serializer changes,
   and keep `TestCanonicalJobFixtureFromTypeScript` byte-exact.
6. Run `go test ./internal/protocol` plus `go test ./...`; when schemas or
   vectors change, also run the extension suite that consumes them
   (`npm test`).
