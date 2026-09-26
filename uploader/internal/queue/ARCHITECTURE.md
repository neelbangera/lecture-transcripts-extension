# queue Architecture

## Purpose

`uploader/internal/queue` is the durable SQLite queue that owns every
acknowledged job. It provides the only `Store` used by the processor: atomic
enqueue-before-ack, deduplication, serial claiming, lease recovery, status
transitions, bounded error metadata, retention pruning, cursor pagination, and a
process-lifetime lock file. No other package writes to the database, and the
database never contains a token or a free-form remote error body.

## Boundaries and dependencies

- Imports `database/sql`, the pure-Go `modernc.org/sqlite` driver, and the
  uploader's `config` (limits, lease, retention) and `protocol` (canonical
  job, status/error vocabularies) packages.
- The processor is the only production caller. Tests open stores directly.
- Storage location and modes come from `config.PathsFor`
  (`~/Library/Application Support/LectureTranscripts/queue.sqlite3`).
- The queue persists the canonical `TranscriptJob` JSON, status, attempt count,
  timestamps, last error category, last HTTP status, and remote inspection
  fields only. It never stores tokens, transcript-derived error text, full
  source URLs beyond the canonical job JSON, or request headers.

## Contracts and invariants

### SQLite schema (version 2)

`uploader/internal/queue/schema.sql` is embedded via `//go:embed` and applied
when `schema_migrations` does not exist. It is already version 2 and includes
`kind`:

```sql
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;

CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY
);

CREATE TABLE jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL DEFAULT 'lecture',
  lecture_key TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  job_json TEXT NOT NULL,
  status TEXT NOT NULL,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT,
  lease_started_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_error_category TEXT,
  last_error_http_status INTEGER,
  remote_content_hash TEXT,
  remote_file_kind TEXT,
  UNIQUE (kind, lecture_key, content_hash),
  CHECK (attempt_count >= 0),
  CHECK (length(content_hash) = 64),
  CHECK (kind IN ('lecture', 'discussion'))
);

CREATE INDEX jobs_ready_idx
  ON jobs (status, next_attempt_at, created_at);

CREATE INDEX jobs_lecture_idx
  ON jobs (kind, lecture_key, created_at);

INSERT INTO schema_migrations(version) VALUES (2);
```

All timestamps are stored as whole-second UTC RFC3339 text (`formatTime` uses
`UTC().Truncate(time.Second).Format(time.RFC3339)`).

### v1 → v2 migration

`migrate` inspects `sqlite_master`; if `schema_migrations` is absent it applies
`schemaSQL` (which stamps version 2). Otherwise it reads
`MAX(version)`:

- `version == 1` → runs `migration2SQL`;
- `version != 2` → returns `ErrUnsupportedSchema` (a future schema is never
  opened);
- `version == 2` → no-op.

`migration2SQL` rebuilds the table because SQLite cannot add the new unique key
in place:

1. `PRAGMA foreign_keys = OFF; BEGIN;`
2. create `jobs_v2` with `kind TEXT NOT NULL DEFAULT 'lecture'`,
   `UNIQUE (kind, lecture_key, content_hash)`, and
   `CHECK (kind IN ('lecture', 'discussion'))`;
3. `INSERT INTO jobs_v2 (...) SELECT id, 'lecture', ... FROM jobs` — every
   existing row becomes a lecture and keeps its `id` and all columns;
4. `DROP TABLE jobs`, `ALTER TABLE jobs_v2 RENAME TO jobs`;
5. recreate `jobs_ready_idx` and `jobs_lecture_idx` (the lecture index now
   starts with `kind`);
6. `INSERT INTO schema_migrations(version) VALUES (2); COMMIT;`
   `PRAGMA foreign_keys = ON;`

`TestMigrationFromVersion1AddsKind` creates a literal v1 database, opens it,
asserts version 2 and `kind='lecture'` for the migrated row, and then enqueues
a discussion.

### Status vocabulary and transitions

Only these values are valid row statuses (`protocol.QueueStatus`):
`queued`, `uploading`, `uploaded`, `unchanged`, `retryable_error`,
`permanent_conflict`, `rejected_missing_identity`,
`rejected_ambiguous_metadata`, `rejected_oversized`, `rejected_queue_full`,
`rejected_invalid_hash`, `rejected_unsafe_url`, `rejected_unknown_field`,
`rejected_invalid_schema`, `rejected_permission`.
`rejected_handoff_full` and `rejected_duplicate_terminal` are never rows.

Transitions implemented by the store. The `Mark*`, `ClaimNext`, `RetryJob`,
`DiscardJob`, `Enqueue`, and `Prune` methods use an explicit transaction;
`PromoteDueRetries` and `RecoverStaleLeases` are single `UPDATE` statements:

| Method | From | To | Side effects |
| --- | --- | --- | --- |
| `Enqueue` | (none) | `queued` | inserts canonical JSON, `attempt_count=0`, `created_at=updated_at` |
| `ClaimNext` | `queued` (and due) | `uploading` | sets `lease_started_at`, `updated_at` |
| `MarkUploaded` | `uploading` | `uploaded` | clears `next_attempt_at`, lease, error metadata; then `Prune` |
| `MarkUnchanged` | `uploading` | `unchanged` | stores validated `remote_content_hash`, `remote_file_kind='file'`; then `Prune` |
| `MarkRetryableError` | `uploading` | `retryable_error` | `attempt_count+1`, `next_attempt_at` required, category/status persisted |
| `MarkPermanentConflict` | `uploading` | `permanent_conflict` | `attempt_count+1`, validates category, remote hash, and remote file kind |
| `MarkRejected` / `MarkRejectedPermission` | `uploading` | `rejected_*` | `attempt_count+1`, `last_error_category` = the status string |
| `PromoteDueRetries` | `retryable_error` | `queued` | `next_attempt_at <= now` only; clears `next_attempt_at` |
| `RecoverStaleLeases` | `uploading` | `queued` | lease expired or null; `attempt_count+1`, clears lease |
| `RetryJob` | `retryable_error`, `permanent_conflict`, `rejected_permission` | `queued` | clears `next_attempt_at` and lease; other statuses return `ErrNotEligible` |
| `DiscardJob` | `permanent_conflict`, `uploaded`, `unchanged`, any `rejected_*` | row deleted | local delete only; never a GitHub delete |

`transition` requires `RowsAffected()==1`; otherwise it returns
`ErrNotEligible`. Invalid transition inputs are rejected before SQL:
unknown error category, HTTP status outside 100–599, zero `next_attempt_at`,
unknown remote file kind, invalid remote hash, and a non-rejection status
passed to `MarkRejected`.

### Enqueue and deduplication

```go
func (s *Store) Enqueue(job protocol.TranscriptJob, now time.Time) (EnqueueResult, error)
```

1. `protocol.CanonicalizeJob(job)` validates and canonicalizes the job (the
   stored source URL is the canonical value, never the raw page URL).
2. `MarshalCanonical()` produces the stored `job_json`.
3. Inside one transaction:
   - lecture identity: `SELECT id, status FROM jobs WHERE kind=? AND
     lecture_key=? AND content_hash=?` — an exact `(kind, lecture_key,
     content_hash)` match is a duplicate;
   - discussion identity (first-capture-wins): any row with the same
     `(kind, lecture_key)` is a duplicate regardless of content hash, so later
     sections of the same discussion are acknowledged as duplicates and never
     raise a conflict;
   - `usageTx` + `wouldExceedLimits`: if `jobs+1 > 500` or
     `bytes+len(payload) > 100 MiB`, return `EnqueueRejectedQueueFull` without
     dropping an existing row;
   - otherwise insert `queued` and commit before the caller acknowledges.

`EnqueueOutcome` values: `EnqueueQueued`, `EnqueueDuplicate`,
`EnqueueDuplicateTerminal`, `EnqueueRejectedQueueFull`. `duplicateResult`
turns a duplicate of a terminal `rejected_*` row into
`EnqueueDuplicateTerminal` with `Action = retry_existing` for
`rejected_permission` and `Action = discard_existing_then_recapture` for every
other rejection. A duplicate of `uploaded`/`unchanged`/active rows returns
`EnqueueDuplicate` with `ExistingStatus` and no action.

### Serial claiming and leases

```go
func (s *Store) ClaimNext(now time.Time) (*Job, error)
```

- The DSN sets `_txlock=immediate`, so every `Begin` takes a write lock; a
  second process cannot interleave a claim.
- The claim selects the oldest `queued` row whose `next_attempt_at` is null or
  due (`ORDER BY created_at ASC, id ASC LIMIT 1`), then updates it to
  `uploading` only `WHERE id=? AND status='queued'`; zero affected rows means
  another claimant won.
- The lease is `config.UploadLease` (10 minutes). `RecoverStaleLeases(now,
  lease)` returns expired or null-lease `uploading` rows to `queued` with
  `attempt_count+1`. `TestClaimSerialAcrossStores` races two stores and
  proves distinct claims; `TestRecoverStaleLeases` proves fresh leases are
  untouched and stale/null leases recover.
- `Open` sets `SetMaxOpenConns(1)`/`SetMaxIdleConns(1)`, so the store
  serializes its own access as well.

### Retention and limits

- At most 500 rows and 100 MiB of `length(CAST(job_json AS BLOB))`.
- After each `MarkUploaded`/`MarkUnchanged`, `Prune` runs. It deletes only
  while the queue is at or over a limit, oldest first, and only rows whose
  status is `uploaded` or `unchanged` and whose `updated_at` is older than
  `now - 7 days`. `queued`, `uploading`, `retryable_error`,
  `permanent_conflict`, and every `rejected_*` row are never auto-deleted.
- A new job that would exceed either limit is rejected as
  `rejected_queue_full`; no existing row is dropped.

### File modes and single-instance lock

- `Open` creates the directory with `0700`, the database with `0600`, and
  re-chmods the database, `-wal`, and `-shm` to `0600` (ignoring `ENOENT`).
- `AcquireLock(path)` creates the directory if needed, opens `queue.lock` with
  `O_RDWR|O_CREATE` and mode `0600`, and takes
  `flock(fd, LOCK_EX|LOCK_NB)`. `EWOULDBLOCK` maps to `ErrAlreadyRunning`.
  `Lock.Release` unlocks and closes; the leftover lock file is harmless.
  `cmd/lecture-uploader` acquires the lock before opening the store.

### Pagination

```go
func (s *Store) StatusPage(beforeJobID *int64, limit int) ([]protocol.JobSummary, *int64, error)
```

- `limit < 1` defaults to 50 and `limit > 50` is clamped to 50.
- The query selects `WHERE id < ?` (when a cursor is supplied), ordered
  `id DESC`, and fetches `limit+1` rows.
- When the extra row exists, the page is truncated to `limit` and
  `nextBeforeJobID` is the `JobID` of the last returned summary; otherwise the
  cursor is nil. The client passes `beforeJobId=nextBeforeJobId` for the next
  page.
- `JobSummary` is built by `Job.Summary()` and carries only bounded fields:
  `jobId`, `lectureKey`, `contentHash`, `status`, `attemptCount`,
  `nextAttemptAt`, `updatedAt`, `targetPath`, `lastErrorCategory`,
  `lastErrorHttpStatus`, `remoteContentHash`, `remoteFileKind`,
  `lectureDate`, `displayTitle`. The last two are the capture's display
  metadata echoed from the job payload so the popup can name a row for a
  student; an empty `displayTitle` is emitted as `null` so the UI synthesizes
  `Lecture N` from the resolved identity. Neither is an identity field.

### Write-once paths

```go
func TargetPath(kind, courseSlug string, lectureNumber int) string
func TimestampedPath(kind, courseSlug string, lectureNumber int) string
```

| Kind | Plain | Timestamped |
| --- | --- | --- |
| `lecture` | `<slug>/<NNN>.md` | `<slug>/timestamped/<NNN>.md` |
| `discussion` | `<slug>/discussions/<NNN>.md` | `<slug>/discussions/timestamped/<NNN>.md` |

`<NNN>` is zero-padded to three digits; the term is part of `lectureKey` but
never part of a repository path.

### Exported errors

`ErrAlreadyRunning`, `ErrJobNotFound`, `ErrNotEligible`,
`ErrUnsupportedSchema`.

## Data flow

```
submit_job
  └─ processor.handleSubmit
       └─ Store.Enqueue(canonical job) ── tx ── dedup ── limits ── INSERT queued ── COMMIT
            └─ ack queued / already_queued / rejected_duplicate_terminal / rejected_queue_full

drain
  └─ Processor.drainOnce
       ├─ Store.PromoteDueRetries(now)
       ├─ Store.RecoverStaleLeases(now, lease)
       └─ Store.ClaimNext(now) ── queued -> uploading
            └─ publisher.Publish(...)
                 ├─ MarkUploaded / MarkUnchanged -> Prune
                 ├─ MarkRetryableError(next_attempt_at)
                 ├─ MarkPermanentConflict(remote fields)
                 └─ MarkRejectedPermission
```

## File responsibilities

| File | Role | Key exports |
| --- | --- | --- |
| `schema.sql` | Canonical version-2 DDL embedded at build time | (embedded `schemaSQL`) |
| `store.go` | Store, migration, transitions, claiming, limits, lock | `ErrAlreadyRunning`, `ErrJobNotFound`, `ErrNotEligible`, `ErrUnsupportedSchema`, `EnqueueOutcome`, `EnqueueQueued`, `EnqueueDuplicate`, `EnqueueDuplicateTerminal`, `EnqueueRejectedQueueFull`, `EnqueueResult`, `Job`, `Store`, `TargetPath`, `TimestampedPath`, `Job.TargetPath`, `Job.TimestampedPath`, `Job.Summary`, `Open`, `Store.Close`, `Store.Path`, `Store.Enqueue`, `Store.ClaimNext`, `Store.MarkUploaded`, `Store.MarkUnchanged`, `Store.MarkRetryableError`, `Store.MarkPermanentConflict`, `Store.MarkRejectedPermission`, `Store.MarkRejected`, `Store.PromoteDueRetries`, `Store.RecoverStaleLeases`, `Store.RetryJob`, `Store.DiscardJob`, `Store.Prune`, `Store.Get`, `Store.Counts`, `Store.StatusPage`, `Store.Usage`, `Lock`, `AcquireLock`, `Lock.Release`, `Lock.Path` |
| `store_test.go` | Schema, migration, dedup, claiming, transitions, retention, lock, pagination tests | `TestOpenCreatesRestrictedFiles`, `TestOpenIsIdempotentAndRejectsFutureSchema`, `TestSchemaColumnsAndIndexes`, `TestEnqueuePersistsCanonicalJobBeforeAck`, `TestEnqueueRejectsInvalidJobWithoutPersisting`, `TestEnqueueDeduplicatesLectureKeyAndHashPair`, `TestEnqueueTerminalDuplicateActions`, `TestEnqueueQueueFullByCount`, `TestWouldExceedLimits`, `TestClaimSerialAcrossStores`, `TestClaimSkipsFutureRetriesUntilDue`, `TestRecoverStaleLeases`, `TestStatusTransitions`, `TestInvalidTransitionsAreRejected`, `TestRetryEligibility`, `TestDiscardRules`, `TestStatusPagePagination`, `TestCountsTrackEveryStatus`, `TestPruneDeletesOnlyOldTerminalRowsUnderPressure`, `TestPruneKeepsRecentRowsAndProtectedStatuses`, `TestAcquireLockIsExclusiveAndReusable`, `TestPlainOnlyTargetPath`, `TestMigrationFromVersion1AddsKind`, `TestDiscussionPathsAndFirstCaptureWins` |

## Testing and verification

Run `cd uploader && go test ./internal/queue/ -count=1`. Highlights:

- `TestOpenCreatesRestrictedFiles` asserts `0700` directory and `0600`
  database/WAL/SHM modes; `TestAcquireLockIsExclusiveAndReusable` asserts the
  `0600` lock mode and exclusivity.
- `TestSchemaColumnsAndIndexes` locks the exact column order and both index
  names; `TestOpenIsIdempotentAndRejectsFutureSchema` proves reopen works and
  a version-3 database returns `ErrUnsupportedSchema`.
- `TestMigrationFromVersion1AddsKind` is the v1→v2 regression test.
- `TestEnqueuePersistsCanonicalJobBeforeAck` proves the stored source URL is
  canonical and paths are `eecs491/006.md` / `eecs491/timestamped/006.md`.
- `TestDiscussionPathsAndFirstCaptureWins` proves discussion paths, first-wins
  dedup across differing hashes, and kind-based disambiguation of a lecture
  with the same numeric identity.
- `TestClaimSerialAcrossStores`, `TestClaimSkipsFutureRetriesUntilDue`,
  `TestRecoverStaleLeases` cover serial claiming and lease semantics.
- `TestStatusTransitions`, `TestInvalidTransitionsAreRejected`,
  `TestRetryEligibility`, `TestDiscardRules` cover the transition table.
- `TestStatusPagePagination` proves `id DESC` ordering, cursor behavior, and
  the 50-row clamp; `TestCountsTrackEveryStatus` proves counts.
- `TestPruneDeletesOnlyOldTerminalRowsUnderPressure` and
  `TestPruneKeepsRecentRowsAndProtectedStatuses` prove retention boundaries.

## Related plan sections

- `TECHNICAL_PLAN.md` → "Canonical SQLite queue schema".
- `TECHNICAL_PLAN.md` → "Transcript job and state model" (identity,
  `lectureKey`, discussion first-wins, paths, statuses, retry behavior).
- `TECHNICAL_PLAN.md` → "Canonical status vocabulary".
- `TECHNICAL_PLAN.md` → "Canonical Native Messaging contract" (JobSummary and
  cursor pagination).
- `TECHNICAL_PLAN.md` → "Stage 4" (durable queue, transactions, serial
  claiming, restart recovery, discard/retry rules).
- `docs/SECURITY.md` → "Local queue data and file modes".

## How to change this package safely

1. Treat `schema.sql` and `migration2SQL` as append-only history. A new schema
   change must be a new migration version; never edit version 2 in place or
   delete a durable row without an explicit user-requested discard.
2. Keep `(kind, lecture_key, content_hash)` as the dedup key for lectures and
   the kind+lecture_key first-wins rule for discussions; the processor and the
   extension's expectations depend on both.
3. Never store free-form remote error text. `validateErrorMeta` accepts only
   the closed category vocabulary and a 100–599 HTTP status.
4. Keep every state transition in a transaction and require
   `RowsAffected()==1`.
5. Do not add a second claim path or a daemon-style poller; the processor owns
   scheduling and the lock file owns single-instance ownership.
6. Any new column requires updating `TestSchemaColumnsAndIndexes`, the
   migration test, and the `scanJob`/`jobSelect` pair together.

Open questions (unverified against the plan text):

- `TECHNICAL_PLAN.md` still prints the version-1 DDL (no `kind`,
  `UNIQUE (lecture_key, content_hash)`, `INSERT ... VALUES (1)`) in "Canonical
  SQLite queue schema", while `schema.sql` in this tree is version 2 with
  `kind` and the migration above. The plan's own identity rules and Stage 6
  paths require `kind`, so the code is consistent with the intent; the plan's
  DDL block is stale.
- The plan's schema section also says "A later migration must be additive",
  but the shipped v1→v2 migration rebuilds the table (copy, drop, rename). It
  preserves every durable row and its `id`; the letter of "additive" is not
  satisfied by a rebuild.
