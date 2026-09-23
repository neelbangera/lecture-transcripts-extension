# processor Architecture

## Purpose

`uploader/internal/processor` is the serial drain that connects the durable
queue, GitHub authorization, Markdown rendering, and write-once publishing. It
owns every job transition, answers Native Messaging requests with queue
decisions, and exposes the closed `drainState` vocabulary. It is deliberately
notifications-agnostic: it never creates a desktop notification, never touches
Chrome APIs, and surfaces terminal outcomes only through queue status, status
counts, `JobSummary` fields, and sanitized local logs.

## Boundaries and dependencies

- Imports `auth`, `config`, `github`, `logging`, `markdown`, `protocol`,
  `queue`, and `retry`.
- Implements two host interfaces (defined in `uploader/internal/host`):
  - `RequestHandler`: `HandleRequest(context.Context, protocol.Request) (any, error)`
  - `SessionLifecycle`: `OnConnect(context.Context) error`, `OnDisconnect()`
- Tests substitute fakes for auth and publishing; no processor test touches a
  real GitHub account, Keychain, or Chrome connection
  (`uploader/internal/IMPLEMENTATION.md` package acceptance).
- The processor does not know about Native Messaging framing, origin
  validation, SQLite internals, Keychain records, HTTP details, or
  notifications. It receives already-validated `protocol.Request` values and
  returns only values `protocol.EncodeResponse` accepts.

## Contracts and invariants

### Composition

```go
type AuthManager interface {
    State() protocol.AuthState
    Challenge() *auth.Challenge
    Begin(ctx context.Context) (*auth.Challenge, error)
    Poll(ctx context.Context) (protocol.AuthState, error)
    Reset() error
}

type Publisher interface {
    InspectFile(ctx context.Context, path string) (github.RemoteFile, error)
    Publish(ctx context.Context, path string, job protocol.TranscriptJob, content []byte) (github.PublishResult, error)
}

type Config struct {
    Store     *queue.Store
    Auth      AuthManager
    Publisher Publisher
    Backoff   retry.Backoff
    Logger    *logging.Logger
    Version   string
    Now       func() time.Time
    Lease     time.Duration
    RetryPollInterval time.Duration
    WriteTimestamped *bool
}

func New(cfg Config) (*Processor, error)
```

`New` requires `Store`, `Auth`, and `Publisher`. Defaults: `Backoff` falls back
to `retry.New()` when its schedule is empty; `Version` defaults to
`DefaultVersion = "dev"` (the build injects the real version with `-ldflags`);
`Now` defaults to `time.Now`; `Lease` defaults to `config.UploadLease` (10m);
`RetryPollInterval` defaults to 1 second; `WriteTimestamped == nil` means
enabled (absent config field). No network, Keychain, or filesystem access
happens in `New`.

### Serial drain lifecycle

- `OnConnect(ctx)`:
  1. `store.RecoverStaleLeases(now, lease)`;
  2. `store.PromoteDueRetries(now)`;
  3. `auth.Begin(ctx)`; an authorization error is logged and exposed through
     status, never returned as a connect failure;
  4. starts exactly one drain goroutine. A second connect while running only
     wakes the existing drain.
- `OnDisconnect()` signals the drain to stop and blocks until the in-flight
  `drainOnce` returns, so a port close never abandons a publish halfway. It
  then clears the session channels. A second connect/disconnect cycle is safe
  (`TestOnConnectProcessesAndDisconnectStops`).
- `run` loops on `drainOnce`:
  - `working` → loop immediately (more work may be ready);
  - `idle` / `waiting_for_backoff` → wait up to `RetryPollInterval` or a wake;
  - `authorizing` → wait up to the current challenge interval (or
    `auth.DefaultPollInterval`) or a wake.
  `HandleRequest` calls `wakeDrain()` after an enqueue, retry, or reset so the
  loop reacts without waiting for the timer.

### `drainOnce`

1. `PromoteDueRetries(now)` and `RecoverStaleLeases(now, lease)`.
2. If `auth.State() == authorizing`, perform exactly one `auth.Poll(ctx)`;
   stay `authorizing` if it is still pending.
3. If auth is not `connected`, return `quiescentState()` without claiming any
   job. Queued jobs stay durable; they are never converted to
   `rejected_permission` by an auth/protocol problem.
4. `store.ClaimNext(now)`; nil means no due work.
5. Process the single job serially and report `working`.

### Drain-state vocabulary

`DrainState()` and `drainOnce` return one of
`idle | working | waiting_for_backoff | authorizing`:

| State | Meaning in code |
| --- | --- |
| `working` | a job is being processed, a `queued` row exists while connected, or a `retryable_error` row is due now |
| `authorizing` | device-code polling is active (`p.polling` or auth state `authorizing`) |
| `waiting_for_backoff` | at least one `retryable_error` row exists and no retry is due |
| `idle` | no active job and no future-due work; terminal rows alone do not prevent idle |

`quiescentState` returns `working` for a connected queue with queued rows,
then `waiting_for_backoff` when `counts.RetryableError > 0`, otherwise `idle`.
`hasDueRetry` inspects one bounded `StatusPage(nil, 50)` so a retry whose
`next_attempt_at` has already passed is reported `working` rather than
`waiting_for_backoff`; rows older than the newest 50 are not inspected between
polls (the next `drainOnce` still promotes them).

### Publish order and outcomes

`processJob` publishes the plain file first and the timestamped file second:

1. `publisher.Publish(job.TargetPath(), job.Payload, markdown.RenderPlain(...))`.
2. `created`/`unchanged` continue; `conflict` → `MarkPermanentConflict`; any
   other outcome → retry as `internal`.
3. If `writeTimestamped` is false **or** `TimestampedTranscript` is blank,
   stop after the plain result and persist `uploaded` (created) or `unchanged`
   (existing same-hash). No timestamped render or PUT happens.
4. Otherwise publish `job.TimestampedPath()` with
   `markdown.RenderTimestamped(...)`; conflict/other outcomes are handled the
   same way.
5. If either file was created, persist `uploaded`; if both were unchanged,
   persist `unchanged` with the best available remote hash (the timestamped
   remote hash when valid, otherwise the job hash).

Because each publish re-inspects its own path, a retry completes a partially
created pair instead of duplicating it. The `writeTimestamped=false` path never
touches the timestamped remote path; enabling the toggle later leaves the plain
file unchanged and creates the missing timestamped file
(`TestWriteTimestampedDisabledPublishesPlainOnly`).

### Failure mapping

`handlePublishError` maps a `*github.Error`:

| GitHub category | Persisted result |
| --- | --- |
| `auth` | `retryable_error` with `last_error_category=reauthorization_required`, HTTP status; the drain then pauses because auth state is no longer `connected` |
| `permission` | `rejected_permission` with HTTP status; terminal |
| `retryable`, `rate_limit` | `retryable_error` with `last_error_category=internal` and HTTP status; delay from `retry.Backoff` |
| anything else (`permanent`) | `permanent_conflict` with `remote_file_kind=malformed` |
| non-`*github.Error` | `retryable_error` with `last_error_category=internal` |

`scheduleRetry` uses the pre-increment `job.AttemptCount` for
`backoff.NextAttemptAt`, so the first failure is 5s. `MarkRetryableError`
increments the stored count. Only `retryable`/`rate_limit` GitHub failures
auto-retry; retries are indefinite and capped at 1 hour.

`markConflict` persists the remote `ContentHash` when present, the remote file
kind (defaulting to `malformed`), and `last_error_category=internal`.

### Submit handling

- A nil job → `ProtocolError{rejected_invalid_schema}`.
- `store.Enqueue` validation errors returned as `protocol.ProtocolError` with a
  submit-ack status (`rejected_invalid_schema`, `rejected_unknown_field`,
  `rejected_oversized`, `rejected_invalid_hash`, `rejected_unsafe_url`) become
  a rejected `protocol.Ack` (no row persisted). An over-limit enqueue returns
  `EnqueueRejectedQueueFull` and also becomes `rejected_queue_full`.
- `EnqueueQueued` → `ack.status=queued` with `jobId`;
  `EnqueueDuplicate` → `already_queued` with `jobId` and `existingStatus`;
  `EnqueueDuplicateTerminal` → `rejected_duplicate_terminal` with `jobId`,
  `existingStatus`, and `action` (`retry_existing` only for
  `rejected_permission`, otherwise `discard_existing_then_recapture`);
  `EnqueueRejectedQueueFull` → `rejected_queue_full`.
- `rejectedSubmitAck` echoes `lectureKey`/`contentHash` only when they pass
  the protocol validators; an invalid hash is never echoed.
- Any other error → `ProtocolError{internal}`.

### Command handlers

| Request | Behavior |
| --- | --- |
| `connect` | the host session lifecycle calls `OnConnect` (recovery, auth resume, drain start); the request branch stores `extensionVersion` and returns a `status` snapshot with the request ID and limit 50 |
| `status_request` | returns `status` with counts, one page (`beforeJobId`, limit default 50/max 50), `drainState`, `authState`, `authorization`, and `nextBeforeJobId`; extension version falls back to `unknown` before connect |
| `retry_job` | nil job ID → `rejected_invalid_schema`; missing job → rejected `invalid_state`; `permanent_conflict` requires a fresh `InspectFile(TargetPath)` that is `missing`, otherwise rejected `ineligible_command` (or the classified GitHub category); eligible rows (`retryable_error`, `permanent_conflict`, `rejected_permission`) move to `queued` and are accepted; other statuses → rejected `ineligible_command` with the current status |
| `discard_job` | nil job ID → `rejected_invalid_schema`; `DiscardJob` accepts only terminal local `rejected_*`, `permanent_conflict`, `uploaded`, `unchanged`; success is `accepted` with status `discarded`; missing job → `invalid_state`; ineligible → `ineligible_command`. Never issues a GitHub delete |
| `reset` | `auth.Reset()`; success is `accepted` with status `reset`; failure is `rejected` with category `internal`. Jobs are untouched |
| unknown type | `ProtocolError{rejected_invalid_schema}` |

`authorization()` filters challenge fields to what the protocol validator
accepts: printable bounded `userCode` (≤64 ASCII), HTTPS `github.com` URIs
(≤2048, no userinfo) for `verificationUri`/`verificationUriComplete`, and a
whole-second RFC3339 `expiresAt`; rejected values are omitted rather than
emitted as an invalid frame.

### Notifications-agnostic behavior

The processor creates no notifications and has no notion of them. Terminal
outcomes (`uploaded`, `unchanged`, `permanent_conflict`, every `rejected_*`)
are visible only as durable rows, status counts, and sanitized logs. The plan
assigns the single desktop notification per terminal job (title
`Lecture uploaded` / `Upload failed`, message limited to `lectureKey` plus the
status label) to the extension, which reads status and decides; `queued`,
`already_queued`, `uploading`, `retryable_error`, and auth/drain states never
notify.

## Data flow

```
connect ──► OnConnect ──► recover leases ──► promote retries ──► auth.Begin ──► drain goroutine
                                                                                  │
drain loop ──► drainOnce ──► [poll auth] ──► ClaimNext ──► processJob             │
                                                              ├─ RenderPlain ──► Publish plain
                                                              ├─ RenderTimestamped ──► Publish timestamped
                                                              └─ MarkUploaded | MarkUnchanged |
                                                                 MarkRetryableError | MarkPermanentConflict |
                                                                 MarkRejectedPermission
submit_job ──► Enqueue ──► ack (queued | already_queued | rejected_duplicate_terminal | rejected_*)
retry_job / discard_job / reset ──► command_result (accepted | rejected)
status_request / connect ──► status (counts, page, drainState, authState, authorization)
```

## File responsibilities

| File | Role | Key exports |
| --- | --- | --- |
| `processor.go` | Composition, serial drain, transitions, command handlers, status snapshots | `DefaultVersion`, `AuthManager`, `Publisher`, `Config`, `Processor`, `New`, `Processor.OnConnect`, `Processor.OnDisconnect`, `Processor.HandleRequest`, `Processor.DrainState` |
| `processor_test.go` | Fake-auth/fake-publisher/real-queue tests plus one host end-to-end test | `TestSubmitEnqueueProcessUploaded`, `TestSubmitDuplicateReturnsAlreadyQueued`, `TestSameHashMarksUnchanged`, `TestDifferentHashMarksPermanentConflict`, `TestMalformedRemoteMarksPermanentConflict`, `TestRetryableErrorSchedulesThenPromotes`, `TestStaleLeaseRecovery`, `TestResetClearsAuthAndKeepsJobs`, `TestResetFailureIsCategoryOnly`, `TestStatusPagination`, `TestConnectStatusEchoesExtensionVersionAndChallenge`, `TestRetryCommandEligibility`, `TestDiscardCommandEligibility`, `TestDrainStateTransitions`, `TestOnConnectProcessesAndDisconnectStops`, `TestInvalidSubmitReturnsRejectionAck`, `TestWriteOnceFixtures`, `TestPermissionErrorMarksRejectedPermission`, `TestAuthErrorPausesDrainAndSchedulesRetry`, `TestStatusRequestBeforeConnectUsesUnknownVersion`, `TestResponseTextNeverCarriesRawErrors`, `TestHostSessionEndToEnd`, `TestPlainOnlyPublishesSingleFile`, `TestWriteTimestampedDisabledPublishesPlainOnly`, `TestDiscussionPublishesToDiscussionsFolder` |

## Testing and verification

Run `cd uploader && go test ./internal/processor/ -count=1`. The suite uses a
real SQLite queue in a temp directory, a `testClock`, `fakeAuth` with a
scripted poll sequence, and an in-memory `fakePublisher`. It proves:

- enqueue → claim → publish → `uploaded`, and duplicate submits returning
  `already_queued` (`TestSubmitEnqueueProcessUploaded`,
  `TestSubmitDuplicateReturnsAlreadyQueued`);
- same-hash → `unchanged`; different/malformed remote → `permanent_conflict`
  with remote fields; the three `testdata` write-once fixtures
  (`TestWriteOnceFixtures`);
- retryable failure → `retryable_error` with 5s delay and `internal` category,
  then promotion to `queued` and success after the clock advances
  (`TestRetryableErrorSchedulesThenPromotes`);
- auth failure schedules a retry with `reauthorization_required` and the drain
  pauses (`TestAuthErrorPausesDrainAndSchedulesRetry`); permission failure
  marks `rejected_permission` (`TestPermissionErrorMarksRejectedPermission`);
- drain-state transitions through idle/authorizing/working/waiting_for_backoff
  (`TestDrainStateTransitions`);
- retry and discard eligibility, including the conflict preflight gate
  (`TestRetryCommandEligibility`, `TestDiscardCommandEligibility`);
- reset clears auth and keeps jobs (`TestResetClearsAuthAndKeepsJobs`);
- plain-only and `writeTimestamped=false` publish exactly one file and a later
  enabled capture creates only the missing timestamped file
  (`TestPlainOnlyPublishesSingleFile`,
  `TestWriteTimestampedDisabledPublishesPlainOnly`);
- discussion paths and both files (`TestDiscussionPublishesToDiscussionsFolder`);
- response payloads never carry raw error text
  (`TestResponseTextNeverCarriesRawErrors`), and responses pass
  `protocol.EncodeResponse` validation via `assertValidResponse`.

## Related plan sections

- `TECHNICAL_PLAN.md` → "Transcript job and state model" (serial processing,
  retryable vs terminal outcomes, conflict handling, `writeTimestamped`).
- `TECHNICAL_PLAN.md` → "Canonical Native Messaging contract" (requests,
  ack/command_result/status shapes, drain lifecycle, retry/discard eligibility).
- `TECHNICAL_PLAN.md` → "Canonical status vocabulary" (terminal outcomes,
  notification ownership, auth-state gating).
- `TECHNICAL_PLAN.md` → "Stage 4" (host lifecycle, serial claiming, retry and
  discard commands) and "Stage 6" (serial publishing).
- `uploader/internal/IMPLEMENTATION.md` (dependency boundaries and
  testability).
- `docs/TROUBLESHOOTING.md` → "Alarm-delayed retries", "Permanent conflicts",
  "Queue-full notices".

## How to change this package safely

1. Keep one drain goroutine per session and keep `drainOnce` as the only unit
   of work; tests drive it directly with a fake clock.
2. Keep the plain-first, timestamped-second order and the "skip timestamped
   when disabled or blank" branch; both are plan contracts.
3. Never claim jobs while auth is not `connected` and never convert them to
   `rejected_permission` because of an auth or protocol problem.
4. Keep `retryable_error` for auth failures rather than a terminal status, and
   keep the `reauthorization_required` category so the popup can react.
5. Keep command results in the exact accepted/rejected shape; add no free-form
   error text to any response.
6. Do not add notification creation or Chrome-specific behavior here; the
   extension owns notifications.
7. When adding a transition, update `processor_test.go` and verify responses
   still pass `protocol.EncodeResponse`.

Open questions (unverified against the plan text):

- The `permanent_conflict` retry preflight (`handleRetry`) inspects only
  `job.TargetPath()` (the plain path). If the conflict is located at the
  timestamped path, the plain file was usually created in the same run, so a
  user who deletes only the timestamped file and presses Retry is rejected as
  `ineligible_command` until the plain file is also removed. `processJob`
  itself does re-inspect both paths once a retry is accepted, which matches
  `TECHNICAL_PLAN.md` Stage 6 ("a retry re-inspects both"); the gate that
  decides eligibility checks one path. This is an observable gap between the
  retry UX and the "re-inspect both" statement, and is not covered by a test.
- `hasDueRetry` inspects only the newest 50 `JobSummary` rows. A due retry
  older than that window can be reported `waiting_for_backoff` until the next
  `drainOnce` promotes it. This is bounded and self-correcting, but it is not
  documented in the plan.
