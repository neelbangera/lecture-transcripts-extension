# uploader Architecture

## Purpose

`uploader/` is the Go module that builds the macOS Native Messaging host
`lecture-uploader`. The process is launched by Chrome through
`dist/native/lecture-uploader` and owns three things the extension must never
own: the durable SQLite queue, GitHub App authorization (device flow +
Keychain), and write-once publishing to the configured repository through the
GitHub Contents API.

- Module path: `github.com/neelbangera/lecture-transcripts-extension/uploader`
  (`go.mod`).
- Toolchain: `go 1.24.0`; the only direct dependency is
  `modernc.org/sqlite v1.38.2` (pure Go). All other modules in `go.mod` are
  `// indirect`.
- Supported build target: `GOOS=darwin GOARCH=arm64` with `CGO_ENABLED=1`,
  because `internal/auth` links `Security.framework` through cgo. The build
  fails closed elsewhere (`scripts/build-uploader.sh`).
- Contract authority: `TECHNICAL_PLAN.md`; `uploader/IMPLEMENTATION.md` and
  `uploader/internal/IMPLEMENTATION.md` record the package order and
  boundaries this module implements.

## Boundaries and dependencies

The normative dependency rule (from `uploader/internal/IMPLEMENTATION.md`) is:

```text
protocol ← host
config → queue, auth, github, logging
queue + auth + github + markdown + retry → processor
processor + host → cmd/lecture-uploader
```

`A ← B` means B imports A; `A → B` means B is built on A. The actual import
edges in the code are:

| Package | Imports (internal) | Notes |
| --- | --- | --- |
| `internal/protocol` | none | closed wire types + validators only |
| `internal/config` | none | constants, paths, machine-local loader |
| `internal/queue` | `config`, `protocol` | SQLite store and lock |
| `internal/auth` | `protocol` | only package allowed to touch Keychain |
| `internal/github` | `protocol` | HTTP client + Contents API |
| `internal/markdown` | `protocol` | deterministic rendering |
| `internal/retry` | `config` | schedule lives in `config` |
| `internal/logging` | `config`, `protocol` | sanitized JSON-lines log |
| `internal/processor` | `auth`, `config`, `github`, `logging`, `markdown`, `protocol`, `queue`, `retry` | serial drain + request handler |
| `internal/host` | `protocol` | framing, origin, lifecycle |
| `cmd/lecture-uploader` | `auth`, `config`, `github`, `host`, `logging`, `processor`, `queue` | composition only |

Hard boundaries that must not be crossed:

- **Only `auth` touches credential storage.** `auth.NewStore()` is the sole
  Keychain entry point (`internal/auth/keychain_darwin.go`,
  `keychain_unsupported.go`). Every other package receives at most an in-memory
  `Authorization: Bearer …` string through `github.CredentialSource`.
- **Transcript text never appears in status or logging types.** Only
  `protocol.TranscriptJob` carries transcript text; `protocol.JobSummary`,
  `protocol.StatusMessage`, and `logging.Fields` have no transcript field.
- **`cmd` only composes tested components.** It wires interfaces and owns no
  queue, network, or rendering logic.
- **`host` never imports `queue`, `auth`, or `github`.** It sees
  `protocol.Request` and opaque `any` responses, validated by
  `protocol.EncodeResponse`.

## Contracts and invariants

### Machine-local paths

`config.PathsFor(homeDir)` derives every runtime path; `config.DefaultPaths()`
resolves the home directory through `os.UserHomeDir()`:

| Path | Constant | Purpose | Mode |
| --- | --- | --- | --- |
| `~/Library/Application Support/LectureTranscripts/` | `config.AppDirName` | data directory | `0700` (`queue.Open`, `queue.AcquireLock`) |
| `.../LectureTranscripts/config.json` | `config.ConfigFileName` | machine-local config | owner-created |
| `.../LectureTranscripts/queue.sqlite3` | `config.QueueFileName` | durable queue (+`-wal`, `-shm`) | `0600` |
| `.../LectureTranscripts/queue.lock` | `config.LockFileName` | single-instance lock | `0600` |
| `~/Library/Logs/LectureTranscripts/` | `config.LogDirName` | log directory | `0700` |
| `~/Library/Logs/LectureTranscripts/uploader.log` | `config.LogFileName` | rotated log (`.1`, `.2`) | `0600` |

`config.LoadFrom` rejects unknown fields, multiple JSON values, and invalid
target values (`owner` must be `neelbangera`, `repo` `lecture-transcripts`,
`branch` `main`; `repositoryId > 0`; a non-placeholder
`githubAppClientId`). `writeTimestamped` is optional and defaults to `true`
via the `optionalBool` wrapper in `config.go`. `config.example.json` is
deliberately nonfunctional and is asserted to fail validation by
`TestExampleConfigIsNonfunctional`.

### Single-instance lock

`queue.AcquireLock(paths.Lock)` opens the lock file `O_RDWR|O_CREAT` (mode
`0600`) and takes `syscall.Flock(fd, LOCK_EX|LOCK_NB)` for the process
lifetime. A second process receives `queue.ErrAlreadyRunning` and `cmd` exits
before opening the queue. `Lock.Release` unlocks and closes; `Lock.Path`
reports the path.

### Queue limits, retention, and durability

Defined once in `config` and enforced in `queue`:

- `QueueMaxJobs = 500`, `QueueMaxBytes = 100 << 20` (100 MiB of stored
  `job_json`).
- `QueueRetention = 7 * 24 * time.Hour`; after each `uploaded`/`unchanged`
  transition the store prunes the oldest such rows older than the retention
  cutoff until under both limits. `queued`, `uploading`, `retryable_error`,
  `permanent_conflict`, and `rejected_*` rows are never auto-pruned.
- A new job that would exceed either limit is rejected as
  `EnqueueRejectedQueueFull` without dropping an existing row.
- Every transition is a transaction; a submit `ack` is emitted only after
  commit (`queue.Enqueue`).
- Queue dedup key: lectures dedup on `(kind, lecture_key, content_hash)`;
  discussions are first-capture-wins on `(kind, lecture_key)`.
- Schema version is 2 (`schema.sql`); `migrate` upgrades a version-1 database
  with `migration2SQL` and refuses anything else with `ErrUnsupportedSchema`.

### Security boundary

- Keychain service `com.neelbangera.lecturetranscripts` with accounts
  `github-app-user-token` and `github-app-device-transaction`
  (`auth.KeychainService`, `CredentialAccount`, `TransactionAccount`), class
  generic password, accessibility
  `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`.
- The credential record is replaced atomically; a failed save leaves the
  previous record untouched (`auth.Manager.refresh`).
- Non-Darwin or non-cgo builds compile `keychain_unsupported.go` and return
  `auth.ErrUnsupportedPlatform`; there is no plaintext fallback.
- The queue stores canonical job JSON and status metadata only — never a
  token (`docs/SECURITY.md`).
- Logs are sanitized JSON lines: closed event/status/category vocabularies,
  `host + path` source only, and `[redacted]` on any sensitive marker
  (`internal/logging/sanitized.go`).
- GitHub errors are persisted as `{category, httpStatus}` only;
  `github.Error.Error()` prints `op`, `category`, and status, never remote
  text (`internal/github/errors.go`).

### stdout/stderr and version

- stdout is reserved for 4-byte little-endian framed protocol data written
  only by `host.WriteFrame`; diagnostics go to the log file
  (`uploader/cmd/lecture-uploader/main.go` package comment).
- `var version = "dev"` in `cmd/lecture-uploader` is replaced at build time by
  `scripts/build-uploader.sh` with `-ldflags "-X main.version=$version"`; the
  script validates the version charset/length and defaults it from
  `package.json`.
- `processor.DefaultVersion = "dev"` is the fallback when the processor is
  constructed without a version.

### Known contradictions / open questions

- **NFC normalization is not implemented in Go.** `TECHNICAL_PLAN.md` line 282
  lists `golang.org/x/text/unicode/norm` as an uploader dependency and line 211
  requires normalization "identical in TypeScript and Go". `uploader/go.mod`
  has no `x/text` dependency, no Go package imports it, and the Go side only
  validates/accepts already-normalized payloads (the normalization vectors are
  exercised by `extension-tests/protocol-vectors.test.ts`). Open question:
  whether Go-side NFC is intentionally deferred to the extension.
- **Plan queue DDL is stale.** The "Canonical SQLite queue schema" section
  (`TECHNICAL_PLAN.md` lines 452–494) shows migration version 1 without the
  `kind` column and with `UNIQUE (lecture_key, content_hash)`. The shipped
  `internal/queue/schema.sql` is version 2 with `kind`, `CHECK (kind IN
  ('lecture','discussion'))`, `UNIQUE (kind, lecture_key, content_hash)`, and a
  code migration `migration2SQL` for existing version-1 databases. The plan's
  job model and path sections (lines 174–181) already describe the v2
  behavior.
- **Stale path in Stage 0 item 10.** `TECHNICAL_PLAN.md` line 677 says only
  `<slug>/lectures/<NNN>.md` may be created, while lines 176–179, `SETUP.md`,
  and `queue.TargetPath` use `<courseSlug>/<NNN>.md` with no `lectures/`
  segment. The code follows the latter.
- **"Additive" migration nuance.** `TECHNICAL_PLAN.md` line 494 says a later
  migration "must never rewrite or delete a durable job"; `migration2SQL`
  rebuilds the `jobs` table with `CREATE`/`INSERT … SELECT`/`DROP`/`RENAME`.
  Every row is preserved, but the table is physically rewritten. Open
  question: whether a rebuild counts as a rewrite under that rule.

## Data flow

1. Chrome starts the host with `argv[1]` = extension origin and a persistent
   stdio port. `cmd.run` resolves paths, opens the log (best-effort), loads and
   validates config, takes the lock, opens the queue, builds `auth.Store` →
   `auth.Manager` → `github.Client`, and constructs `processor.Processor`.
2. `cmd.readAllowedOrigin` reads the installed Chrome manifest
   (`~/Library/Application Support/Google/Chrome/NativeMessagingHosts/
   com.neelbangera.lecturetranscripts.json`), requires exactly one
   `allowed_origins` entry, and re-validates it with `host.ValidateOrigin`.
3. `host.Server.Run` validates the origin, then loops: `ReadFrame` →
   `protocol.DecodeRequest` → optional `Lifecycle.OnConnect` for `connect` →
   `Handler.HandleRequest` → `protocol.EncodeResponse` → `WriteFrame`.
   Invalid requests never reach the processor; `OnDisconnect` runs on exit.
4. `processor.HandleRequest` answers `submit_job` by enqueuing (canonicalized,
   validated) and waking the drain; it answers `status_request`,
   `retry_job`, `discard_job`, and `reset` from queue/auth state.
5. The single drain goroutine (`Processor.run` → `drainOnce`) promotes due
   retries, recovers stale leases, polls an active device flow, claims the
   oldest queued job, renders Markdown, and publishes plain-then-timestamped
   through `github.Client.Publish`. Outcomes become durable queue transitions
   (`uploaded`, `unchanged`, `retryable_error`, `permanent_conflict`,
   `rejected_permission`).
6. GitHub requests obtain a fresh header from `auth.Manager` via
   `github.CredentialSource`; on 401 the client forces exactly one refresh.
7. Every state change that matters is logged through `logging.Logger` to the
   rotated file; stdout carries only framed responses.

## File responsibilities

| File / directory | Role | Key exports |
| --- | --- | --- |
| `go.mod`, `go.sum` | module definition and locked checksums; `modernc.org/sqlite` is the only direct dependency | module path, `go 1.24.0` |
| `IMPLEMENTATION.md` | normative build order and required behavior for this module | — |
| `ARCHITECTURE.md` | this document | — |
| `cmd/lecture-uploader/main.go` | process assembly, manifest origin read, signal handling | `main`, `run`, `readAllowedOrigin`, `version` (ldflags target) |
| `internal/protocol/` | closed wire types and validators | see `internal/protocol/ARCHITECTURE.md` |
| `internal/host/` | Native Messaging framing, origin allowlist, session loop | see `internal/host/ARCHITECTURE.md` |
| `internal/config/` | machine-local config, limits, paths, backoff schedule | `Config`, `Paths`, `DefaultPaths`, `LoadFrom`, `BackoffSchedule`, limit constants |
| `internal/queue/` | SQLite store, lock, dedup, leases, transitions, pagination | `Store`, `Open`, `AcquireLock`, `Job`, `TargetPath`, `TimestampedPath` |
| `internal/auth/` | Keychain store, device flow, token refresh | `Manager`, `NewManager`, `NewStore`, `Store`, `Credential` |
| `internal/github/` | authenticated Contents API client, write-once publish | `Client`, `NewClient`, `Publish`, `Category`, `Error` |
| `internal/markdown/` | deterministic plain/timestamped rendering | `RenderPlain`, `RenderTimestamped`, `PublishSourceURL` |
| `internal/retry/` | stateless capped backoff | `Backoff`, `New`, `NewWithSource` |
| `internal/processor/` | serial drain and request handler | `Processor`, `New`, `Config`, `AuthManager`, `Publisher` |
| `internal/logging/` | sanitized rotated JSON-lines log | `Logger`, `Open`, `Event`, `Fields`, `Redacted` |
| `testdata/*.md` | remote-file fixtures consumed by `processor.TestWriteOnceFixtures` | `existing-same-hash.md`, `existing-different-hash.md`, `malformed-lecture.md` |

## Testing and verification

```sh
cd uploader
go build ./...
go vet ./...
go test ./...
```

Every package except `cmd/lecture-uploader` has a `_test.go` file (see the
per-package index in `internal/ARCHITECTURE.md`); `go test ./...` passes with
no network access because GitHub, auth HTTP, and the clock are injectable.
`cmd/lecture-uploader` has no test files of its own: the host/processor seam it
composes is covered by `processor.TestHostSessionEndToEnd` and the
`internal/host` server tests. Packaging behavior (manifest rendering, version
injection, dry-run, unsupported platform) is covered by
`sh scripts/tests/run.sh` and `docs/TESTING.md`.

## Related plan sections

- Toolchain baseline — `TECHNICAL_PLAN.md` lines 279–285.
- Canonical Native Messaging contract — lines 287–390.
- Canonical transcript-job schema — lines 392–436.
- Canonical source-URL vectors — lines 438–450.
- Canonical SQLite queue schema — lines 452–494 (see stale-DDL note above).
- Canonical status vocabulary — lines 496–537.
- Canonical normalization-vector contract — lines 539–642.
- Stage 4 (host + queue) — lines 883–903; Stage 5 (auth) — 905–922;
  Stage 6 (rendering/publishing) — 924–979; Stage 7 (packaging) — 981–998.
- Native uploader file inventory — lines 1087–1121; build/install files —
  1123–1131; generated/machine-local artifacts — 1143–1163.

## How to change this directory safely

1. Read `TECHNICAL_PLAN.md` first; the plan is normative for field names,
   limits, statuses, and paths. If a change alters a contract, update the plan
   and the TypeScript side before the Go code.
2. Keep the dependency direction intact: `protocol` stays dependency-free;
   `host` never learns about queue/auth/GitHub; only `auth` may touch
   Keychain; `cmd` stays composition-only.
3. Add or update unit tests with fakes before changing behavior. The
   processor must remain testable without a real Keychain, GitHub account, or
   Chrome connection.
4. Do not add dependencies beyond the toolchain baseline without updating the
   plan; `go.mod` currently allows only `modernc.org/sqlite` directly.
5. When a wire type changes, update `protocol/*.schema.json`, the TypeScript
   client, and `internal/protocol/testdata/transcript-job.canonical.json`
   together, and keep `TestCanonicalJobFixtureFromTypeScript` green.
6. Never print to stdout outside `host.WriteFrame`; never log transcript,
   tokens, queries, or remote bodies; never commit the binary, queue
   database, config, or logs (`dist/`, `*.sqlite3*`, `*.log` are ignored).
7. Run `go build ./... && go vet ./... && go test ./...` plus
   `sh scripts/tests/run.sh` before considering a change complete.
