# Protocol Architecture

## Purpose

`protocol/` holds the machine-readable wire contract shared by the Chrome
extension, the Go uploader, and both test suites: the Native Messaging
request/response schema, the transcript job schema, and the cross-language
normalization and source-URL vectors. It is the one place where a wire change
is recorded, so TypeScript and Go cannot drift apart silently.

These JSON files are contracts and test data. No runtime process reads them:
the TypeScript and Go validators are hand-written and are kept in parity by
tests that read these files. [`TECHNICAL_PLAN.md`](../TECHNICAL_PLAN.md) is the
normative authority; where a schema or vector disagrees with the plan, the plan
wins and the file must be corrected deliberately before implementation
continues (`protocol/IMPLEMENTATION.md`).

## Boundaries and dependencies

Dependency direction:

```text
TECHNICAL_PLAN.md (normative prose)
   -> protocol/*.json (machine-readable contract + vectors)
        -> extension-tests/*.test.ts        (TypeScript validators under test)
        -> uploader/internal/protocol/*_test.go (Go validators under test)
```

- Producers: the extension builds `TranscriptJob` values
  (`extension/src/transcript-job.ts`, `extension/src/transcript-normalizer.ts`)
  and Native Messaging envelopes (`extension/src/native-messaging.ts`).
- Consumer: the Go uploader decodes and validates every request
  (`uploader/internal/protocol/validate.go`), and the host/processor answer
  with the response shapes defined here.
- Test consumers:
  - `extension-tests/protocol-vectors.test.ts` imports
    `normalization-vectors.json` and `source-url-vectors.json`, and reads the
    Go fixture `uploader/internal/protocol/testdata/transcript-job.canonical.json`.
  - `uploader/internal/protocol/job_test.go` reads
    `protocol/source-url-vectors.json` and the canonical fixture via
    `readProtocolFile` (`../../..` from the package directory).
  - `uploader/internal/protocol/schema_test.go` reads
    `protocol/native-messaging.schema.json` and
    `protocol/transcript-job.schema.json` to assert schema/Go parity.
- The extension build (`scripts/build-extension.mjs`) does not read
  `protocol/`; it embeds the Stage 0 selector fixture instead.
- No package may add a general RPC, a second status vocabulary, or a new
  message field outside these files.

## Contracts and invariants

### `native-messaging.schema.json`

- JSON Schema draft 2020-12; `protocolVersion` is the constant `1`.
- Every envelope sets `additionalProperties: false`; `requestId` is 1-64
  printable ASCII characters (`^[ -~]{1,64}$`).
- Transcript text may occur only in `submit_job.job`; status and error
  messages never carry it.
- Requests (`oneOf` titles): `ConnectRequest`, `SubmitJobRequest`,
  `StatusRequest` (`beforeJobId` positive cursor, `limit` 1-50 default 50),
  `RetryJobRequest`, `DiscardJobRequest` (requires `confirmation: "discard"`),
  `ResetRequest`.
- Responses: `SubmitAck` (`ack`; `operation: "submit"`; echoes `jobId`,
  `lectureKey`, `contentHash`; `status` is `queued`, `already_queued`, or one
  of the seven submit rejections; `existingStatus`; `action` is `null`,
  `retry_existing`, or `discard_existing_then_recapture`),
  `CommandResult` (retry/discard/reset), `Status`, and `Error`.
- Shared `$defs`: `queueStatus` (15 persisted values), `submitRejectionStatus`
  (7 values; `rejected_duplicate_terminal` is ack-only), `errorCategory` (19
  values), `authState` (6), `drainState` (`idle`, `working`,
  `waiting_for_backoff`, `authorizing`), `remoteFileKind` (`file`,
  `directory`, `symlink`, `submodule`, `malformed`, `missing`), `jobSummary`
  (12 required fields, including `targetPath` <=512 and nullable remote
  fields), and `counts` (all 15 queue-status keys required, nonnegative).
- `Status.jobs` is capped at 50 items and carries `nextBeforeJobId` as the
  next cursor; `Status.requestId` and `Error.requestId` may be `null` only for
  unsolicited status messages.

### `transcript-job.schema.json`

- 13 required fields in canonical order: `schemaVersion`, `kind`,
  `lectureKey`, `courseSlug`, `courseName`, `term`, `lectureNumber`,
  `lectureDate`, `sourceUrl`, `capturedAt`, `transcript`,
  `timestampedTranscript`, `contentHash`; `additionalProperties: false`.
- Patterns: `lectureKey ^[a-z0-9]+/[0-9]{4}-(winter|spring|summer|fall)/[0-9]{3}$`,
  `courseSlug ^[a-z0-9]+$`, `term ^[0-9]{4}-(winter|spring|summer|fall)$`,
  `lectureDate ^\d{4}-\d{2}-\d{2}$`, `capturedAt` whole-second RFC3339 UTC,
  `contentHash ^[0-9a-f]{64}$`.
- The schema's `$comment` records that runtime validation additionally enforces
  UTF-8 byte limits (460800 per transcript field, 972800 for the canonical
  serialized job) because JSON Schema `maxLength` counts characters, and that
  `contentHash` is the SHA-256 of the framed `transcript-hash-v1` input.
- Runtime validation beyond the schema (both languages): real calendar date,
  `lectureKey` consistency with the other identity fields, at least 50
  non-whitespace characters of normalized transcript, and the source-URL
  host/path rules.

### `normalization-vectors.json`

Ten vectors with fields `id`, `plainInput`, `timestampedInput`,
`expectedPlain`, `expectedTimestamped`, and `hashAssertion`. The assertion
vocabulary is `normalized-self`, `different-from:<id>`, and `same-as:<id>`.
They pin NFC normalization (`e\u0301lan` == `élan`), CRLF/whitespace
collapsing, blank-line collapse, that word changes and timestamp-digit changes
change the framed hash, and that an empty versus populated timestamped form
changes it.

### `source-url-vectors.json`

Three vectors with fields `id`, `input`, `expectedCanonicalUrl`,
`expectedPublishSourceUrl`, `expectedAction`, and an optional `expectedError`:

| Case | Expected |
| --- | --- |
| Uppercase host, `:443`, query, fragment | Canonical `https://leccap.engin.umich.edu/lecture/123`; publish included |
| Sensitive path segment (`token`) | Canonical URL unchanged; publish omitted (`null`) |
| `http://` scheme | Rejected as `rejected_unsafe_url` |

The canonicalization rules are: require `https`, exact lowercase host
`leccap.engin.umich.edu`, no userinfo, no port other than `:443`, strip the
default port, drop query and fragment, keep the encoded path (empty path
becomes `/`), and reject over 2048 UTF-8 bytes. The publish rule additionally
omits `source_url` when a lowercased path segment (split on `/._-`) is a whole
segment in `token|session|auth|sid`. The file may add observed-path cases but
may not change these rules or make an unsafe input valid.

### Cross-language canonical fixture

`uploader/internal/protocol/testdata/transcript-job.canonical.json` is the
byte-for-byte canonical serialization of a TypeScript-built job
(`extension-tests/protocol-vectors.test.ts` asserts equality) that the Go
protocol package must parse, validate, re-serialize, and byte-match
(`uploader/internal/protocol/job_test.go`). It pins canonical property order,
compact JSON (no insignificant whitespace, no trailing newline), and raw
escaping of `&`, `<`, and `>`.

## Data flow

1. The extension normalizes both transcript forms and computes `contentHash`
   with the framed sequence.
2. `serializeTranscriptJob` produces compact canonical JSON in schema order;
   the same serialization is used for byte measurement and test vectors.
3. The job travels inside `submit_job.job`; the Go decoder rejects duplicate
   keys, unknown fields, trailing JSON, wrong types, invalid UTF-8, invalid
   hashes, unsafe URLs, oversized fields, and inconsistent `lectureKey`.
4. Responses are validated by the extension's `isNativeResponse` before use;
   the Go side validates outgoing messages against the same vocabulary in
   tests (`response_test.go`, `schema_test.go`).
5. Vector files flow only into tests: both languages run the same
   normalization and URL vectors and must produce identical results.

## File responsibilities

| File | Role |
| --- | --- |
| `native-messaging.schema.json` | Canonical strict request/response envelopes: version, correlation, status pagination, drain lifecycle, rejection actions, shared `$defs` |
| `transcript-job.schema.json` | Canonical `TranscriptJob` schema; documents the byte-cap and framed-hash rules in its `$comment` |
| `normalization-vectors.json` | Shared NFC/whitespace/blank-line/timestamp vectors and hash relationship assertions |
| `source-url-vectors.json` | Shared canonicalization and publish-sanitization vectors |
| `IMPLEMENTATION.md` | Directory work plan: review against the plan, test every request/response type, run vectors from both languages, confirm fail-closed behavior, plan wins on disagreement |
| `ARCHITECTURE.md` | This document |

## Testing and verification

Run both sides from the repository root:

```sh
npm test                                   # extension-tests/protocol-vectors.test.ts and job tests
cd uploader && go test ./internal/protocol/...
```

What is covered:

- `extension-tests/protocol-vectors.test.ts`: exact normalized outputs and
  fixed points, exact framed UTF-8 bytes, every `hashAssertion`, every URL
  vector, `rejected_unsafe_url` mapping through job validation, and the
  cross-language fixture byte equality, hash recomputation, compactness, and
  raw escaping.
- `extension-tests/transcript-job.test.ts`: identity/path derivation, limits,
  canonical serialization, URL rules, and invalid jobs.
- `extension-tests/native-messaging.test.ts`: envelope validation,
  unknown-field rejection, port reuse, response correlation.
- `uploader/internal/protocol/job_test.go`: source-URL vectors, canonical job
  serialization and field order, hash framing, byte boundaries, canonical
  fixture parity.
- `uploader/internal/protocol/schema_test.go`: schema vocabulary matches the
  Go enums and struct fields; `additionalProperties: false`; request/response
  type coverage.
- `uploader/internal/protocol/request_test.go` and `response_test.go`: every
  request and response type, including fail-closed cases.

Verified while writing this document: `go test ./...` passes in
`uploader/internal/protocol`. The TypeScript vector suite was not re-run here
because `node_modules/` is not installed in this worktree.

## Related plan sections

- § Normative implementation contracts
- § Canonical Native Messaging contract
- § Canonical transcript-job schema
- § Canonical source-URL vectors
- § Canonical normalization-vector contract
- § Canonical status vocabulary
- § Canonical SQLite queue schema (queue DDL cross-reference)
- § Canonical Native Messaging host template
- § Stage 1 acceptance checks; § Stage 2 and Stage 4 acceptance checks

## How to change this directory safely

- Change `TECHNICAL_PLAN.md` first (and `PHASE_1_DECISIONS.md` when the
  rationale changes). Never edit a schema or vector to make failing code pass.
- Change the schema, both language validators, both test suites, and the
  canonical fixture in one deliberate change; `protocol/IMPLEMENTATION.md`
  requires that a protocol change cannot pass in one language and fail in the
  other.
- Never relax a pattern, cap, status enum, or URL rule without a plan edit; do
  not add fields, request types, or a general RPC.
- Keep vectors and schemas free of secrets, real extension IDs, tokens, or raw
  page captures.
- After a change, run `npm test`, `cd uploader && go test ./...`, and
  `sh scripts/tests/run.sh` if packaging files were touched.

### Open questions and known inconsistencies

- `TECHNICAL_PLAN.md` § Canonical SQLite queue schema still shows migration
  version 1 with `UNIQUE (lecture_key, content_hash)` and no `kind` column,
  while `uploader/internal/queue/schema.sql` is migration version 2 with
  `kind` and `UNIQUE (kind, lecture_key, content_hash)`. The wire contract
  itself has `kind`; the plan's DDL block is the stale part.
- `protocol/IMPLEMENTATION.md` says cross-language conformance tests are
  missing; they exist in this tree (canonical fixture, schema/Go parity,
  shared vectors in both languages).
- The plan's Stage 0 item 10 and `docs/TROUBLESHOOTING.md` still mention a
  `<slug>/lectures/<NNN>.md` lecture path, which no current schema or Go path
  derivation uses (the plan's own identity section and `queue/store.go` use
  `<slug>/<NNN>.md`). This is a prose inconsistency, not a wire-schema field.
- `source-url-vectors.json` declares `expectedError` only on the reject case;
  a consumer must treat its absence on include/omit cases as "no error", which
  the current tests do.
