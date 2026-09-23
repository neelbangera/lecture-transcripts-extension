# internal Architecture

## Purpose

`uploader/internal/` holds every isolated component of the uploader. The
directory is the enforcement point for the module's dependency rules: wire
types and validators, transport framing, machine-local configuration, durable
queue, credentials, GitHub publishing, Markdown rendering, retry math, the
serial processor, and sanitized logging. Nothing here is an executable; the
only command is `uploader/cmd/lecture-uploader`.

This document is the per-package index. Two packages have their own
architecture documents (`host`, `protocol`); the rest are documented here and
in their source files.

## Boundaries and dependencies

Normative rules (`uploader/internal/IMPLEMENTATION.md`):

```text
protocol ← host
config → queue, auth, github, logging
queue + auth + github + markdown + retry → processor
processor + host → cmd/lecture-uploader
```

- `protocol` and `config` import no other internal package.
- `queue`, `auth`, `github`, `markdown`, and `logging` may import `protocol`
  and/or `config` but never each other (except through `processor`).
- `processor` is the only package that composes queue + auth + GitHub +
  Markdown + retry; it exposes narrow interfaces (`AuthManager`, `Publisher`)
  so tests use fakes.
- `host` knows only `protocol`; `cmd` is composition-only.
- No package may expose transcript text in status/logging types, and no package
  other than `auth` may access credential storage.

## Contracts and invariants

- **One source for limits.** `protocol` defines the wire limits
  (`MaxFrameBytes`, `MaxSerializedJobByte`, `MaxTranscriptBytes`,
  `MaxSourceURLBytes`) and `config` re-declares the application-facing copies
  (`MaxSerializedJobBytes`, `MaxTranscriptBytes`, `MaxSourceURLBytes`,
  `MaxRenderTimeMs`). `config.TestLimitsMatchProtocolContract` fails if they
  drift.
- **One source for the retry schedule.** `config.BackoffSchedule()` returns
  `5s, 30s, 2m, 10m, 1h`; `config.BackoffJitterFraction = 0.20`;
  `retry.Backoff` reads both and defines no second schedule.
- **Errors are category-only.** `protocol.ProtocolError`, `github.Error`, and
  `logging` never carry raw JSON, transcript text, remote bodies, or
  credentials. Persisted error metadata is `{category, httpStatus}` only.
- **Canonical job JSON is the queue representation.** `queue.Enqueue` calls
  `protocol.CanonicalizeJob` + `MarshalCanonical` before insert; the stored
  `job_json` is byte-stable and shared with the TypeScript client fixture.
- **Filesystem modes.** Queue directory `0700`, queue/WAL/SHM/lock/log `0600`,
  log directory `0700` (see `uploader/ARCHITECTURE.md` for the path table).
- **No plaintext credentials.** `auth.NewStore` is the only credential entry
  point; `keychain_unsupported.go` fails closed off-Darwin/without cgo.

## Data flow

```text
cmd → host.Server → protocol.DecodeRequest → processor.HandleRequest
                                              ├─ queue.Store (SQLite)
                                              ├─ auth.Manager (Keychain, device flow)
                                              ├─ markdown render
                                              └─ github.Client (Contents API, retry)
processor → protocol.EncodeResponse → host.WriteFrame → stdout
all components → logging.Logger → ~/Library/Logs/LectureTranscripts/uploader.log
```

`queue` and `auth` never talk to GitHub; `github` never reads the Keychain;
`host` never sees a queue row or a token.

## File responsibilities

| Directory | Role | Key exports (summary) |
| --- | --- | --- |
| `config/` | machine-local config, limits, paths, backoff schedule | `Config`, `Paths`, `LoadFrom`, `DefaultPaths`, constants |
| `queue/` | SQLite store, lock, dedup, leases, pagination | `Store`, `Open`, `AcquireLock`, `Job`, `TargetPath` |
| `auth/` | Keychain store, GitHub device flow, token refresh | `Manager`, `NewManager`, `NewStore`, `Store` |
| `github/` | authenticated Contents API + write-once publish | `Client`, `NewClient`, `Publish`, `Category`, `Error` |
| `markdown/` | deterministic plain/timestamped rendering | `RenderPlain`, `RenderTimestamped`, `PublishSourceURL` |
| `retry/` | stateless capped backoff | `Backoff`, `New`, `NewWithSource` |
| `processor/` | serial drain + host request handler | `Processor`, `New`, `Config`, `AuthManager`, `Publisher` |
| `logging/` | sanitized rotated JSON-lines log | `Logger`, `Open`, `Event`, `Fields` |
| `host/` | Native Messaging framing, origin, lifecycle | `Server`, `NewServer`, `ReadFrame`, `WriteFrame`, `ValidateOrigin` |
| `protocol/` | closed wire types and validators | `TranscriptJob`, `Request`, `StatusMessage`, `EncodeResponse` |

### config

Role: loads and validates `~/Library/Application Support/LectureTranscripts/
config.json`, owns the shared constants (limits, paths, retention, backoff),
and derives all machine-local paths.

Exported API surface (`config/config.go`):

- Constants: `SchemaVersion = 1`; `MaxSerializedJobBytes = 972800`;
  `MaxTranscriptBytes = 460800`; `MaxSourceURLBytes = 2048`;
  `MaxRenderTimeMs = 30000`; `QueueMaxJobs = 500`;
  `QueueMaxBytes = 100 << 20`; `QueueRetention = 7 * 24 * time.Hour`;
  `UploadLease = 10 * time.Minute`; `LogMaxBytes = 5 << 20`;
  `LogMaxFiles = 3`; `BackoffJitterFraction = 0.20`; `ExpectedOwner`,
  `ExpectedRepo`, `ExpectedBranch`; `AppDirName`, `LogDirName`,
  `ConfigFileName`, `QueueFileName`, `LockFileName`, `LogFileName`.
- Functions: `BackoffSchedule() []time.Duration`, `PathsFor(homeDir string)
  Paths`, `DefaultPaths() (Paths, error)`, `Load() (Config, error)`,
  `LoadFrom(path string) (Config, error)`.
- Types: `Config` (`SchemaVersion`, `GitHubAppClientID`, `RepositoryID`,
  `Owner`, `Repo`, `Branch`, `WriteTimestamped`) with
  `(Config).Validate() error`; `Paths` (`Dir`, `Config`, `Queue`, `Lock`,
  `LogDir`, `Log`).

Detailed doc: `uploader/ARCHITECTURE.md` (paths, lock, security) plus
`config/config.go` and `config/config.example.json`; tests in
`config/config_test.go` (`TestLoadValidConfig`, `TestWriteTimestampedConfig`,
`TestExampleConfigIsNonfunctional`, `TestLimitsMatchProtocolContract`,
`TestBackoffScheduleContract`, `TestQueueAndLoggingLimits`,
`TestPathsForHome`).

### queue

Role: the durable SQLite queue and the single-instance lock. Owns schema
migration (v1 → v2), dedup, leases, transitions, pruning, and status pages.

Exported API surface (`queue/store.go`):

- Errors: `ErrAlreadyRunning`, `ErrJobNotFound`, `ErrNotEligible`,
  `ErrUnsupportedSchema`.
- Types: `EnqueueOutcome` (`EnqueueQueued`, `EnqueueDuplicate`,
  `EnqueueDuplicateTerminal`, `EnqueueRejectedQueueFull`); `EnqueueResult`
  (`Outcome`, `JobID`, `ExistingStatus`, `Action`); `Job` (`ID`,
  `LectureKey`, `ContentHash`, `Payload`, `Status`, `AttemptCount`,
  `NextAttemptAt`, `LeaseStartedAt`, `CreatedAt`, `UpdatedAt`,
  `LastErrorCategory`, `LastErrorHTTPStatus`, `RemoteContentHash`,
  `RemoteFileKind`); `Store`; `Lock`.
- Functions: `TargetPath(kind, courseSlug string, lectureNumber int) string`,
  `TimestampedPath(...)`, `Open(path string) (*Store, error)`,
  `AcquireLock(path string) (*Lock, error)`.
- Methods: `(*Store) Close/Path/Enqueue/ClaimNext/MarkUploaded/MarkUnchanged/
  MarkRetryableError/MarkPermanentConflict/MarkRejectedPermission/MarkRejected/
  PromoteDueRetries/RecoverStaleLeases/RetryJob/DiscardJob/Prune/Get/Counts/
  StatusPage/Usage`; `(Job) TargetPath/TimestampedPath/Summary`;
  `(*Lock) Release/Path`.
- Embedded schema: `queue/schema.sql` (version 2) and the unexported
  `migration2SQL`.

Detailed doc: `uploader/ARCHITECTURE.md` (limits, dedup, security) plus
`queue/store.go`; tests in `queue/store_test.go` (restricted files, schema
columns/indexes, canonical persistence before ack, dedup, terminal duplicate
actions, queue-full, serial claiming, stale-lease recovery, transitions,
retry/discard eligibility, pagination, counts, pruning, lock exclusivity,
migration from v1, discussion first-capture-wins).

### auth

Role: the only package allowed to touch credential storage. Owns the GitHub
App device flow, resumable device transaction, token refresh, and the connect
sanity checks.

Exported API surface:

- Constants (`auth/auth.go`): `KeychainService`, `CredentialAccount`,
  `TransactionAccount`. (`auth/web_flow.go`): `DefaultDeviceCodeURL`,
  `DefaultTokenURL`, `DefaultAPIBaseURL`, `DefaultRefreshThreshold`,
  `DefaultPollInterval`.
- Errors: `ErrNoCredential`, `ErrNoTransaction`, `ErrUnsupportedPlatform`,
  `ErrNotConnected`, `ErrReauthorizationRequired`,
  `ErrTargetRepositoryUnavailable`.
- Types: `Credential` (`AccessToken`, `RefreshToken`,
  `AccessTokenExpiresAt`, `RefreshTokenExpiresAt`, `TokenType`,
  `RepositoryID`, `RepositoryFullName`); `DeviceTransaction` (`DeviceCode`,
  `UserCode`, `VerificationURI`, `VerificationURIComplete`, `ExpiresAt`,
  `Interval`); `Challenge` (`UserCode`, `VerificationURI`,
  `VerificationURIComplete`, `ExpiresAt`, `Interval`); `Store` interface
  (`LoadCredential`, `SaveCredential`, `DeleteCredential`, `LoadTransaction`,
  `SaveTransaction`, `DeleteTransaction`); `RepositoryVerifier` interface
  (`VerifyRepository`); `State = protocol.AuthState`; `Config` (`ClientID`,
  `RepositoryID`, `Owner`, `Repo`, `Branch`, endpoint overrides,
  `HTTPClient`, `Now`, `Sleep`, `RefreshThreshold`, `Verifier`); `Manager`;
  `HTTPRepositoryVerifier` (`BaseURL`, `Owner`, `Repo`, `Branch`,
  `RepositoryID`, `HTTPClient`).
- Functions: `NewManager(store Store, cfg Config) (*Manager, error)`;
  `NewStore() (Store, error)` (build-tagged: cgo Darwin keychain, otherwise
  `ErrUnsupportedPlatform`).
- Methods: `(*Manager) State/Challenge/Begin/Poll/Connect/AuthorizationHeader/
  ForceRefresh/Reset`; `(*HTTPRepositoryVerifier) VerifyRepository`.

Detailed doc: `auth/auth.go`, `auth/web_flow.go`, `auth/keychain_darwin.go`,
`auth/keychain_unsupported.go`; tests in `auth/auth_test.go` (device flow
persistence/resume/expiry, slow_down, terminal errors, refresh threshold,
forced refresh, reset, verifier headers) and
`auth/keychain_unsupported_test.go`.

### github

Role: authenticated, bounded GitHub REST requests and the write-once publish
state machine.

Exported API surface:

- Constants (`github/client.go`): `DefaultBaseURL`, `DefaultRequestTimeout`
  (30s), `DefaultMaxResponseBytes` (1 MiB).
- Types: `CredentialSource` interface (`AuthorizationHeader`, `ForceRefresh`);
  `ClientConfig` (`BaseURL`, `Owner`, `Repo`, `Branch`, `Credentials`,
  `HTTPClient`, `RequestTimeout`, `MaxResponseBytes`); `Client`; `Repository`
  (`ID`, `FullName`, `DefaultBranch`); `RemoteFile` (`Path`, `Kind`,
  `BlobSHA`, `ContentHash`, `Size`); `PublishOutcome` (`OutcomeCreated`,
  `OutcomeUnchanged`, `OutcomeConflict`); `PublishResult` (`Outcome`,
  `Remote`, `HTTPStatus`); `CreateResult` (`Path`, `BlobSHA`, `CommitSHA`);
  `Category` (`CategoryRetryable`, `CategoryRateLimit`, `CategoryAuth`,
  `CategoryPermission`, `CategoryPermanent`); `Error` (`Category`,
  `HTTPStatus`, `Op`).
- Functions: `NewClient(cfg ClientConfig) (*Client, error)`;
  `CommitMessage(job protocol.TranscriptJob, path string) string`;
  `ParseTranscriptHash(content []byte) (string, bool)`.
- Methods: `(*Client) Branch/GetRepository/InspectFile/CreateFile/Publish`;
  `(Category) Retryable/ProtocolCategory`; `(*Error) Error/Retryable`.

Detailed doc: `github/client.go`, `github/contents.go`, `github/errors.go`;
tests in `github/contents_test.go` (create without sha, unchanged revisit,
different/malformed/directory/symlink conflicts, race rechecks, rate limit,
auth refresh-once, permission/404 classification, retryable network/timeout,
oversized response, sanitized `Error()` text, hash parsing, https-only base
URL, commit message format).

### markdown

Role: deterministic Markdown rendering for the two machine-owned files, with
source-URL publish sanitization.

Exported API surface (`markdown/render.go`):

- Constant: `NoTimestampedSource = "_No timestamped source; see Transcript._"`.
- Functions: `RenderPlain(job protocol.TranscriptJob) []byte`;
  `RenderTimestamped(job protocol.TranscriptJob) []byte`;
  `PublishSourceURL(raw string) (string, bool)`.

Detailed doc: `markdown/render.go`; tests in `markdown/render_test.go`
(golden bytes, one-line-per-entry joining, empty timestamped source,
unsafe-URL omission, metadata escaping, determinism, trailing-newline
normalization, URL vectors).

### retry

Role: stateless capped backoff. The attempt count lives in the queue row, so a
restart cannot lose the schedule position.

Exported API surface (`retry/backoff.go`):

- Type: `Backoff` (unexported `schedule`, `jitter`, `source`).
- Functions: `New() Backoff`; `NewWithSource(source func() float64) Backoff`.
- Methods: `(Backoff) Schedule() []time.Duration`; `BaseDelay(attemptCount
  int) time.Duration`; `Delay(attemptCount int) time.Duration`;
  `NextAttemptAt(attemptCount int, now time.Time) time.Time`.

Detailed doc: `retry/backoff.go`; tests in `retry/backoff_test.go` (schedule
matches config, delay follows schedule, cap after the last entry, negative
attempt count, jitter bounds/extremes, provided clock, reset behavior,
injectable source).

### processor

Role: the single serial drain and the host `RequestHandler`/`SessionLifecycle`
implementation. Owns every job transition decision.

Exported API surface (`processor/processor.go`):

- Constant: `DefaultVersion = "dev"`.
- Interfaces: `AuthManager` (`State`, `Challenge`, `Begin`, `Poll`, `Reset`);
  `Publisher` (`InspectFile`, `Publish`).
- Types: `Config` (`Store`, `Auth`, `Publisher`, `Backoff`, `Logger`,
  `Version`, `Now`, `Lease`, `RetryPollInterval`, `WriteTimestamped`); `Processor`.
- Function: `New(cfg Config) (*Processor, error)`.
- Methods: `(*Processor) OnConnect(ctx) error`, `OnDisconnect()`,
  `HandleRequest(ctx, protocol.Request) (any, error)`,
  `DrainState() protocol.DrainState`.

Detailed doc: `processor/processor.go` and `uploader/ARCHITECTURE.md` (data
flow); tests in `processor/processor_test.go` (submit→upload, duplicate acks,
same-hash unchanged, conflicts, retry scheduling/promotion, lease recovery,
reset, pagination, connect challenge, retry/discard eligibility, drain-state
transitions, host session end-to-end, permission/auth error handling, plain-only
and `writeTimestamped=false` behavior, discussion paths).

### logging

Role: bounded, privacy-safe structured JSON-lines logging with rotation.

Exported API surface (`logging/sanitized.go`):

- Constant: `Redacted = "[redacted]"`.
- Types: `Level` (`LevelInfo`, `LevelWarn`, `LevelError`); `Event` (21
  constants: `EventStartup`, `EventShutdown`, `EventConfigLoaded`,
  `EventLockAcquired`, `EventLockAlreadyRunning`, `EventQueueOpened`,
  `EventJobEnqueued`, `EventJobDuplicate`, `EventJobQueueFull`,
  `EventJobClaimed`, `EventJobUploaded`, `EventJobUnchanged`,
  `EventJobRetryableError`, `EventJobPermanentConflict`, `EventJobRejected`,
  `EventRetryPromoted`, `EventLeaseRecovered`, `EventJobsPruned`,
  `EventAuthState`, `EventGitHubRequest`, `EventProtocolError`); `Fields`
  (`LectureKey`, `CourseSlug`, `Term`, `LectureNumber`, `Status`,
  `ErrorCategory`, `SourceURL`); `Logger`.
- Function: `Open(path string) (*Logger, error)`.
- Methods: `(*Logger) Close/Path/Info/Warn/Error`.

Detailed doc: `logging/sanitized.go` and `docs/SECURITY.md` (redaction rules);
tests in `logging/sanitized_test.go` (restricted path, allowed fields,
sensitive-sample redaction, source sanitization, rotation to three files,
idempotent close).

### host

Role: Chrome Native Messaging framing, origin allowlist, and the persistent
session loop. Detailed architecture: `internal/host/ARCHITECTURE.md`.

Exported API surface (`host/native_messaging.go`):

- Constant: `MaxFrameBytes = protocol.MaxFrameBytes` (1 MiB).
- Errors: `ErrFrameTooLarge`, `ErrMalformedFrame`, `ErrOriginNotConfigured`,
  `ErrOriginMismatch`, `ErrInvalidHandler`.
- Types: `FrameError` (`Kind`, `ClaimedLength`, `Cause`);
  `RequestHandler` interface (`HandleRequest`); `SessionLifecycle` interface
  (`OnConnect`, `OnDisconnect`); `Server` (`Reader`, `Writer`, `Handler`,
  `Lifecycle`, `Origin`, `AllowedOrigin`).
- Functions: `NewServer(reader, writer, handler, lifecycle, origin,
  allowedOrigin) *Server`; `ReadFrame(io.Reader) ([]byte, error)`;
  `WriteFrame(io.Writer, []byte) error`; `ExtensionOrigin(extensionID string)
  (string, error)`; `ValidateOrigin(origin, allowedOrigin string) error`;
  `FormatOriginArgument(args []string) (string, error)`.
- Method: `(*Server) Run(ctx context.Context) error`.

Tests: `host/native_messaging_test.go` (framing endianness, short reads/writes,
EOF, truncation, exact limit, oversized claimed/emitted frames, origin
matching, server loop, lifecycle, validation routing, serialization, stdout
purity).

### protocol

Role: the closed wire contract shared with the TypeScript client. Detailed
architecture: `internal/protocol/ARCHITECTURE.md`.

Exported API surface (see that document for the full validation rules):

- Constants: `ProtocolVersion = 1`, `MaxFrameBytes`, `MaxSerializedJobByte`,
  `MaxTranscriptBytes`, `MaxSourceURLBytes`, `KindLecture`, `KindDiscussion`;
  request/ack/duplicate/auth/drain/remote/error vocabularies.
- Types: `TranscriptJob` (+ `MarshalCanonical`), `Request` (+ `EffectiveLimit`,
  `MarshalJSON`), `Ack`, `CommandResult`, `Authorization`, `QueueCounts`,
  `JobSummary`, `StatusMessage`, `ErrorMessage`, `ProtocolError`.
- Functions: `NewProtocolError`, `DecodeRequest`, `ValidateJob`,
  `CanonicalizeJob`, `CanonicalizeSourceURL`, `EncodeResponse`,
  `DecodeResponse`, `ValidateResponse`, `IsQueueStatus`, `IsErrorCategory`,
  `IsAuthState`, `IsDrainState`, `IsRemoteFileKind`, `IsValidLectureKey`,
  `IsValidContentHash`.
- Cross-language fixture: `protocol/testdata/transcript-job.canonical.json`.

Tests: `job_test.go`, `request_test.go`, `response_test.go`, `schema_test.go`,
`helpers_test.go`.

## Testing and verification

```sh
cd uploader
go build ./...
go vet ./...
go test ./...
```

All ten `internal` packages have tests; `go test ./...` passes offline because
external state is injected (fake HTTP servers, fake stores, fake clocks,
in-memory pipes). Verification of the contracts is layered:

- `config.TestLimitsMatchProtocolContract` keeps config/protocol limits equal.
- `protocol/schema_test.go` reads `protocol/*.schema.json` and compares the
  Go vocabularies and `TranscriptJob` struct against the schemas.
- `protocol/job_test.go` verifies the TypeScript-produced canonical fixture
  byte-for-byte and re-derives its framed SHA-256.
- `processor.TestHostSessionEndToEnd` exercises the host ↔ processor seam.
- `queue/store_test.go` covers migration v1 → v2 and lock exclusivity.
- `sh scripts/tests/run.sh` covers packaging; `docs/TESTING.md` is the full
  test map.

## Related plan sections

- Package boundaries and planned packages — `TECHNICAL_PLAN.md` lines 287–390
  (Native Messaging contract), 392–436 (job schema), 452–494 (queue schema),
  496–537 (status vocabulary), 539–642 (normalization vectors).
- Stage 4 (host + queue) — lines 883–903; Stage 5 (auth) — 905–922;
  Stage 6 (render/publish) — 924–979.
- Native uploader file inventory — lines 1087–1121.
- Internal package plan — `uploader/internal/IMPLEMENTATION.md`.

## How to change this directory safely

1. Preserve the dependency direction. A new package must declare where it sits
   in the graph and must not create a cycle; `host` may never import `queue`,
   `auth`, or `github`.
2. Put contract constants in exactly one place: wire limits in `protocol`,
   application limits/paths/schedule in `config`. Add a cross-check test if you
   add a second representation.
3. Keep every package testable with fakes. Do not let a package require the
   real Keychain, a real GitHub account, a real clock, or Chrome.
4. Keep status and logging types transcript-free; never add raw error text to
   a wire type or a log line. Use `protocol.ErrorCategory` and
   `github.Category`.
5. Any wire change is a three-way change: `internal/protocol`,
   `protocol/*.schema.json`, and the TypeScript client/fixtures — plus this
   document and the per-package docs.
6. Run `go build ./... && go vet ./... && go test ./...` and, when touching
   limits or vocabulary, the extension suite (`npm test`) that consumes the
   shared protocol files.
