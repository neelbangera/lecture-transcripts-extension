# logging Architecture

## Purpose

`uploader/internal/logging` is the uploader's only log writer. It emits
JSON-lines records with an allowlisted field set to
`~/Library/Logs/LectureTranscripts/uploader.log`, rotates them at 5 MiB across
at most three files, and redacts anything that is not part of a closed
vocabulary or that contains a known sensitive marker. Transcript text, tokens,
cookies, device codes, full source URLs, query strings, and fragments can never
be written through it.

## Boundaries and dependencies

- Imports `encoding/json`, `errors`, `fmt`, `net/url`, `os`, `path/filepath`,
  `regexp`, `strings`, `sync`, `time`, and the uploader's `config` (rotation
  limits) and `protocol` (status/error/auth/drain vocabularies) packages.
- Consumers: `cmd/lecture-uploader` and `processor` call `Info`/`Warn`/`Error`
  with a `logging.Event` and `logging.Fields`. `processor` treats a nil
  `*Logger` as "logging disabled" (`logEvent`/`logStatus`/`logError` return
  early), so the processor is testable without a log file.
- The package writes only to the configured log path; it never writes to
  stderr, never sends anywhere else, and never opens a network connection.
- Chrome stderr is explicitly not the troubleshooting log
  (`docs/TROUBLESHOOTING.md` points at the file).

## Contracts and invariants

### Location, modes, rotation

| Item | Value |
| --- | --- |
| Directory | `~/Library/Logs/LectureTranscripts` (`config.PathsFor(...).LogDir`) |
| Current file | `uploader.log` |
| Rotated files | `uploader.log.1`, `uploader.log.2` (at most three files total) |
| Directory mode | `0700` (`os.MkdirAll` + `os.Chmod`) |
| File mode | `0600` (`O_APPEND|O_CREATE|O_WRONLY` plus explicit `Chmod`) |
| Rotation threshold | `config.LogMaxBytes` = `5 << 20` (5 MiB) |
| Max files | `config.LogMaxFiles` = 3 |

`rotateLocked` runs before every write. It rotates only when the current file
is non-empty, `size+pending > maxBytes`, and `maxFiles >= 2`. It removes
`uploader.log.<maxFiles-1>`, shifts `.i` to `.i+1` from the highest index down,
renames the current file to `.1`, and reopens (re-appending) the current path
with mode `0600`. Because the incoming line is counted in `pending`, no file
exceeds the threshold by a full line. A rotation or write error is swallowed:
logging is best-effort and must never take down the uploader.

### Allowed fields

The JSON record has exactly these keys; every optional key uses `omitempty`:

```go
type entry struct {
    Time          string `json:"time"`
    Level         Level  `json:"level"`
    Event         string `json:"event"`
    LectureKey    string `json:"lectureKey,omitempty"`
    CourseSlug    string `json:"courseSlug,omitempty"`
    Term          string `json:"term,omitempty"`
    LectureNumber int    `json:"lectureNumber,omitempty"`
    Status        string `json:"status,omitempty"`
    ErrorCategory string `json:"errorCategory,omitempty"`
    Source        string `json:"source,omitempty"`
}
```

`time` is whole-second UTC RFC3339 from the logger's clock. `Fields` carries
`LectureKey`, `CourseSlug`, `Term`, `LectureNumber`, `Status`, `ErrorCategory`,
and `SourceURL`; the last is reduced to a host+path `source` string before
writing.

Validation rules per field:

| Field | Rule when written | Otherwise |
| --- | --- | --- |
| `lectureKey` | `protocol.IsValidLectureKey` | `[redacted]` |
| `courseSlug` | `^[a-z0-9]{1,64}$` | `[redacted]` |
| `term` | `^[0-9]{4}-(winter\|spring\|summer\|fall)$` | `[redacted]` |
| `lectureNumber` | 1–999 | `0` (omitted) |
| `status` | queue status, auth state, or drain state | `[redacted]` |
| `errorCategory` | ≤64 chars and a known protocol error category | `[redacted]` |
| `event` | `^[a-z][a-z0-9_]{0,31}$` | `unknown` |

### Redaction

`Redacted = "[redacted]"`. `containsSensitive` lowercases the value and matches
any of these markers:

`transcript_body`, `transcript=`, `token=`, `token:`, `access_token`,
`refresh_token`, `sessionid`, `session_id`, `cookie`, `authorization`,
`bearer `, `device_code`, `user_code`.

A field value that fails its vocabulary check or contains a marker becomes
`[redacted]`; an event name that is malformed or sensitive becomes `unknown`.
`TestRedactsSensitiveSamples` feeds a sensitive lecture key, course slug, term,
status, error category, source URL, and event name and asserts that none of the
samples (including `WDJB-MJHT`, `ghp_secret_value`, `?`, `#`) appear in the
file while `leccap.engin.umich.edu/lecture/123` and `[redacted]` do.

### Source sanitization

`sanitizeSource(raw)`:

- empty input → empty;
- `url.Parse` failure, empty host, userinfo, or opaque form → empty;
- scheme must be `https` or `http` (logging is less strict than publishing);
- returns `lowercase(host) + escapedPath` with an empty path normalized to
  `/`;
- if the result still contains a sensitive marker → `[redacted]`;
- query and fragment are never part of the result.

`TestSanitizeSource` covers query+fragment stripping, uppercase host, empty
path, `http`, userinfo, other schemes, non-URLs, empty input, and a sensitive
path segment. `TestLogsStructuredAllowedFields` asserts a URL containing
`?token=abc#frag` logs only `leccap.engin.umich.edu/lecture/123` and that the
record contains no unexpected key.

### Events

`Event` constants (all lowercase, matching the event pattern):
`startup`, `shutdown`, `config_loaded`, `lock_acquired`,
`lock_already_running`, `queue_opened`, `job_enqueued`, `job_duplicate`,
`job_queue_full`, `job_claimed`, `job_uploaded`, `job_unchanged`,
`job_retryable_error`, `job_permanent_conflict`, `job_rejected`,
`retry_promoted`, `lease_recovered`, `jobs_pruned`, `auth_state`,
`github_request`, `protocol_error`.

Levels: `LevelInfo = "info"`, `LevelWarn = "warn"`, `LevelError = "error"`.

### API

```go
func Open(path string) (*Logger, error)
func (l *Logger) Close() error
func (l *Logger) Path() string
func (l *Logger) Info(event Event, fields Fields)
func (l *Logger) Warn(event Event, fields Fields)
func (l *Logger) Error(event Event, fields Fields)
```

`Open` rejects an empty path, creates the directory `0700`, opens the file
`0600`, and records its current size so rotation continues across restarts.
`Close` is idempotent and safe on a nil receiver. `Info`/`Warn`/`Error` are
safe on a nil receiver (no-op), which is what lets the processor run without a
logger. The logger holds one mutex around rotation and writing, so concurrent
callers cannot interleave lines.

## Data flow

```
processor / cmd
  └─ Logger.Info|Warn|Error(Event, Fields)
       ├─ encode: validate/redact each field, sanitize source, stamp UTC time
       ├─ rotateLocked: shift .1 -> .2, uploader.log -> .1, reopen 0600
       └─ append one JSON line to uploader.log (best-effort; errors ignored)
```

## File responsibilities

| File | Role | Key exports |
| --- | --- | --- |
| `sanitized.go` | Logger, field allowlist, redaction, source sanitization, rotation | `Redacted`, `Level`, `LevelInfo`, `LevelWarn`, `LevelError`, `Event`, all `Event*` constants, `Fields`, `Logger`, `Open`, `Logger.Close`, `Logger.Path`, `Logger.Info`, `Logger.Warn`, `Logger.Error` |
| `sanitized_test.go` | Mode, structure, redaction, source, rotation, close tests | `TestOpenCreatesRestrictedLogPath`, `TestLogsStructuredAllowedFields`, `TestRedactsSensitiveSamples`, `TestSanitizeSource`, `TestRotationKeepsAtMostThreeRestrictedFiles`, `TestCloseIsIdempotentAndSafe` |

## Testing and verification

Run `cd uploader && go test ./internal/logging/ -count=1`. The suite proves:

- directory `0700` and file `0600`, and an empty path fails
  (`TestOpenCreatesRestrictedLogPath`);
- exactly the allowlisted fields are emitted with the expected JSON values, and
  query/fragment never appear (`TestLogsStructuredAllowedFields`);
- sensitive samples, device/user codes, tokens, and transcript markers are
  redacted or replaced (`TestRedactsSensitiveSamples`);
- source reduction rules (`TestSanitizeSource`);
- with `maxBytes=300`/`maxFiles=3`, 30 records leave exactly three files, each
  `0600` and `<= 300` bytes, and the newest record survives in the current or
  `.1` file (`TestRotationKeepsAtMostThreeRestrictedFiles`);
- `Close` is idempotent, nil-safe, and leaves the file on disk
  (`TestCloseIsIdempotentAndSafe`).

## Related plan sections

- `TECHNICAL_PLAN.md` → "Stage 4" item 7 (log path, `0600`, 5 MiB rotation
  across three files, allowed fields, source host+path only, redaction tests).
- `TECHNICAL_PLAN.md` → "Canonical status vocabulary" (closed vocabularies).
- `docs/SECURITY.md` → "Log redaction and rotation".
- `docs/TROUBLESHOOTING.md` → "Log location and reading".

## How to change this package safely

1. Never add a free-form field to `entry` or `Fields`; new data must fit an
   existing allowlisted key or be redacted. Status, auth, and drain values are
   the only status strings accepted.
2. Never remove a sensitive marker or weaken `containsSensitive`; add markers
   when a new secret shape appears.
3. Keep the source reduction to host+path. Query strings and fragments are
   never logged, regardless of scheme.
4. Keep rotation bounded and modes exact; any change to file count or size
   requires updating `config` limits, this doc, `docs/SECURITY.md`, and
   `TestRotationKeepsAtMostThreeRestrictedFiles`.
5. Keep logging best-effort: a write or rotation failure must not panic or
   propagate to callers.
6. Keep `Close` idempotent and nil-receiver methods no-ops; the processor
   relies on nil-tolerant logging. No open questions remain: allowed fields,
   redaction markers, source rules, modes, and rotation behavior are directly
   implemented in `sanitized.go` and asserted by `sanitized_test.go`.
