# config Architecture

## Purpose

`uploader/internal/config` is the single loader for the machine-local,
non-secret provisioning file and the single source of the uploader's numeric
limits, path layout, and backoff schedule. It exists so that no other package
hardcodes a target repository, invents a limit, or silently accepts a
placeholder configuration. Loading is fail-closed: a missing, malformed,
incomplete, placeholder, or mismatched file returns an error and the process
must stop rather than guess.

## Boundaries and dependencies

- Imports only the Go standard library (`bytes`, `encoding/json`, `errors`,
  `fmt`, `io`, `os`, `path/filepath`, `regexp`, `strings`, `time`). It has no
  dependency on any other uploader package, and no dependency on the Keychain,
  SQLite, or the network.
- Consumers: `cmd/lecture-uploader` (paths, limits), `queue` (queue limits and
  lease), `logging` (rotation limits), `processor` (`UploadLease`,
  `BackoffSchedule` via `retry`), `auth`/`github` receive the validated target
  values from the command wiring.
- `config` never stores or returns a secret. `githubAppClientId` is public;
  `repositoryId` is not sensitive but is still owner-specific and is never
  committed.
- Per `uploader/internal/IMPLEMENTATION.md`, the dependency direction is
  `config → queue, auth, github, logging`; no package may expose transcript text
  and no package other than `auth` may touch credential storage.

## Contracts and invariants

### Exact `config.json` shape

`~/Library/Application Support/LectureTranscripts/config.json`:

```json
{
  "schemaVersion": 1,
  "githubAppClientId": "real App client ID supplied during setup",
  "repositoryId": 123456789,
  "owner": "neelbangera",
  "repo": "lecture-transcripts",
  "branch": "main",
  "writeTimestamped": true
}
```

`writeTimestamped` is optional. Its semantics are fixed:

- absent means `true`;
- explicit `true`/`false` is honored;
- `null`, a string, or a number is rejected;
- it changes only what the uploader publishes (plain-only when false), never the
  job payload, its content hash, or the plain file's write-once identity.

### Fail-closed rules (`Config.Validate`)

`Validate` rejects:

- `schemaVersion != 1`;
- a `githubAppClientId` that does not match
  `^[A-Za-z0-9][A-Za-z0-9._-]{7,127}$`;
- a client ID containing a placeholder marker, case-insensitively:
  `replace`, `placeholder`, `changeme`, `your_`, `example`;
- `repositoryId <= 0`;
- `owner != "neelbangera"`, `repo != "lecture-transcripts"`,
  `branch != "main"` (constants `ExpectedOwner`, `ExpectedRepo`,
  `ExpectedBranch`).

`decode` additionally rejects unknown fields (`DisallowUnknownFields`), a
second trailing JSON value ("multiple JSON values"), and an empty/malformed
document. `LoadFrom` reads the file (a read failure is wrapped as
`read machine-local config: ...`) and validates the decoded value (a
validation failure is wrapped as `invalid machine-local config: ...`); there is
no default and no partial load.

### Exported constants

| Constant | Value | Meaning |
| --- | --- | --- |
| `SchemaVersion` | `1` | accepted config schema |
| `MaxSerializedJobBytes` | `972800` | compact job JSON cap (950 KiB) |
| `MaxTranscriptBytes` | `460800` | per-transcript cap (450 KiB) |
| `MaxSourceURLBytes` | `2048` | canonical source URL cap |
| `MaxRenderTimeMs` | `30000` | Stage 0 render budget |
| `QueueMaxJobs` | `500` | durable queue row cap |
| `QueueMaxBytes` | `100 << 20` | stored job-JSON cap (100 MiB) |
| `QueueRetention` | `7 * 24 * time.Hour` | retention for uploaded/unchanged rows |
| `UploadLease` | `10 * time.Minute` | `uploading` lease |
| `LogMaxBytes` | `5 << 20` | log rotation threshold |
| `LogMaxFiles` | `3` | `uploader.log`, `.1`, `.2` |
| `BackoffJitterFraction` | `0.20` | ±20% jitter |
| `AppDirName` / `LogDirName` | `"LectureTranscripts"` | directory names |
| `ConfigFileName` | `"config.json"` | config file name |
| `QueueFileName` | `"queue.sqlite3"` | queue database name |
| `LockFileName` | `"queue.lock"` | single-instance lock name |
| `LogFileName` | `"uploader.log"` | log file name |

`BackoffSchedule()` returns a fresh slice
`[5s, 30s, 2m, 10m, 1h]`. `retry.Backoff` consumes it; the config package is
the only schedule definition.

### Path derivation

```go
func PathsFor(homeDir string) Paths
func DefaultPaths() (Paths, error)
func Load() (Config, error)
func LoadFrom(path string) (Config, error)
```

`PathsFor(home)` derives:

| Field | Path |
| --- | --- |
| `Dir` | `<home>/Library/Application Support/LectureTranscripts` |
| `Config` | `<Dir>/config.json` |
| `Queue` | `<Dir>/queue.sqlite3` |
| `Lock` | `<Dir>/queue.lock` |
| `LogDir` | `<home>/Library/Logs/LectureTranscripts` |
| `Log` | `<LogDir>/uploader.log` |

`DefaultPaths` uses `os.UserHomeDir()` and rejects an empty result.
`Load()` = `DefaultPaths()` + `LoadFrom(paths.Config)`.

### Types

```go
type Config struct {
    SchemaVersion     int    `json:"schemaVersion"`
    GitHubAppClientID string `json:"githubAppClientId"`
    RepositoryID      int64  `json:"repositoryId"`
    Owner             string `json:"owner"`
    Repo              string `json:"repo"`
    Branch            string `json:"branch"`
    WriteTimestamped  bool   `json:"writeTimestamped"`
}
```

Internally `wireConfig` + `optionalBool` exist solely so an absent
`writeTimestamped` can default to `true` while explicit `false` is preserved;
`optionalBool.UnmarshalJSON` rejects `null` explicitly.

## Data flow

```
config.Load()
  └─ DefaultPaths() ── os.UserHomeDir() ── PathsFor(home)
  └─ LoadFrom(paths.Config)
       ├─ os.ReadFile          (missing file -> error, fail closed)
       ├─ decode               (DisallowUnknownFields, single JSON value,
       │                        optionalBool defaulting)
       └─ Config.Validate      (schema, client ID, repo ID, target values)
             └─ Config (in memory) ── cmd wiring ── queue/auth/github/logging/processor
```

`config.example.json` is documentation only. `TestExampleConfigIsNonfunctional`
decodes it structurally but asserts `Validate()` fails and that `LoadFrom`
fails, so the example can never be used as a runtime config.

## File responsibilities

| File | Role | Key exports |
| --- | --- | --- |
| `config.go` | Exact loader, validator, constants, path derivation | `SchemaVersion`, `MaxSerializedJobBytes`, `MaxTranscriptBytes`, `MaxSourceURLBytes`, `MaxRenderTimeMs`, `QueueMaxJobs`, `QueueMaxBytes`, `QueueRetention`, `UploadLease`, `LogMaxBytes`, `LogMaxFiles`, `BackoffJitterFraction`, `ExpectedOwner`, `ExpectedRepo`, `ExpectedBranch`, `AppDirName`, `LogDirName`, `ConfigFileName`, `QueueFileName`, `LockFileName`, `LogFileName`, `BackoffSchedule`, `Config`, `Paths`, `PathsFor`, `DefaultPaths`, `Load`, `LoadFrom`, `Config.Validate` |
| `config.example.json` | Nonfunctional shape documentation with placeholder values; intentionally invalid at runtime | (data file) |
| `config_test.go` | Loader, fail-closed, path, and contract-limit tests | `TestLoadValidConfig`, `TestWriteTimestampedConfig`, `TestLoadRejectsInvalidConfig`, `TestLoadRejectsMissingFile`, `TestExampleConfigIsNonfunctional`, `TestPathsForHome`, `TestDefaultPathsUseHomeEnvironment`, `TestLimitsMatchProtocolContract`, `TestBackoffScheduleContract`, `TestQueueAndLoggingLimits` |

## Testing and verification

Run `cd uploader && go test ./internal/config/ -count=1`. The suite verifies:

- a valid file loads with the exact target values
  (`TestLoadValidConfig`);
- `writeTimestamped` absent/true/false behavior (`TestWriteTimestampedConfig`);
- every rejection case: empty, malformed, wrong schema, missing/short/
  placeholder client ID, zero/negative/string repository ID, wrong owner/repo/
  branch, unknown field, string/numeric/null `writeTimestamped`, trailing JSON
  value (`TestLoadRejectsInvalidConfig`);
- a missing file fails closed (`TestLoadRejectsMissingFile`);
- the example config is structurally decodable but nonfunctional
  (`TestExampleConfigIsNonfunctional`);
- path derivation and `$HOME` handling (`TestPathsForHome`,
  `TestDefaultPathsUseHomeEnvironment`);
- limits equal the protocol contract and the backoff schedule is exactly
  `5s,30s,2m,10m,1h` with 0.20 jitter and a fresh slice on every call
  (`TestLimitsMatchProtocolContract`, `TestBackoffScheduleContract`,
  `TestQueueAndLoggingLimits`).

## Related plan sections

- `TECHNICAL_PLAN.md` → "Required provisioning packet" (exact config shape and
  fail-closed rules).
- `TECHNICAL_PLAN.md` → "Normative implementation contracts" and "Toolchain
  baseline" (Go 1.24.x, standard library plus `modernc.org/sqlite` and
  `golang.org/x/text/unicode/norm`).
- `TECHNICAL_PLAN.md` → "Transcript job and state model" (backoff schedule and
  queue retention).
- `TECHNICAL_PLAN.md` → "Stage 4" (DB path, queue limits, log rotation) and
  "Generated and machine-local artifacts" (the config path).
- `docs/SETUP.md` → "Write the machine-local configuration".
- `docs/SECURITY.md` → "Local queue data and file modes".

## How to change this package safely

1. Do not relax validation to make a local file load. Fix the file instead.
   Fail-closed behavior is a plan guardrail.
2. If a limit changes, change `TECHNICAL_PLAN.md` first; the config constants
   are asserted against `protocol` limits by `TestLimitsMatchProtocolContract`.
   Do not introduce a config field for a limit, path, or retry interval: the
   plan states those come from the contracts, not mutable config.
3. Keep `writeTimestamped` semantics exact: absent = true, explicit boolean
   only, no effect on the job payload or content hash.
4. Update `config.example.json` and `TestExampleConfigIsNonfunctional`
   together; the example must stay structurally complete but invalid at
   runtime.
5. Keep `PathsFor` the only place that derives filesystem locations so setup,
   security, and troubleshooting docs stay consistent.
