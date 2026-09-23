# cmd/lecture-uploader Architecture

## Purpose

`uploader/cmd/lecture-uploader` is the `package main` entrypoint for the macOS
Native Messaging host. It contains no business logic: it resolves machine-local
paths, loads and validates configuration, takes the single-instance lock, opens
the durable queue, assembles the auth/GitHub/processor stack, reads the
installer-rendered origin, and hands stdin/stdout to `host.Server` for the
lifetime of the Chrome port. It is the only place in the module where concrete
implementations are wired to interfaces.

- File: `uploader/cmd/lecture-uploader/main.go` (the package's only file).
- Built by `scripts/build-uploader.sh` to `dist/native/lecture-uploader`
  (`CGO_ENABLED=1`, `GOOS=darwin`, `GOARCH=arm64`,
  `-trimpath -ldflags "-X main.version=$version"`).

## Boundaries and dependencies

Imports (stdlib plus internal only):

```text
context, encoding/json, errors, fmt, io, os, os/signal, path/filepath, syscall
internal/auth, internal/config, internal/github, internal/host,
internal/logging, internal/processor, internal/queue
```

- `cmd` may import anything below it; nothing imports `cmd`.
- The host boundary is injected, not imported by name: `*processor.Processor`
  satisfies both `host.RequestHandler` (`HandleRequest`) and
  `host.SessionLifecycle` (`OnConnect`, `OnDisconnect`), so `host` stays free of
  queue/auth/GitHub knowledge.
- `github.NewClient` receives `auth.Manager` as its `github.CredentialSource`;
  the raw credential never appears in `cmd`.
- There are no test files in this package; it must remain small enough to be
  verified by inspection plus the component tests.

## Contracts and invariants

### Startup assembly order (`run`)

`main` calls `run(os.Args[1:], os.Stdin, os.Stdout)`; `run` assembles in this
exact order, and every failure returns before a protocol byte is written:

1. `config.DefaultPaths()` — resolves the home directory and the six
   machine-local paths.
2. `logging.Open(paths.Log)` — best-effort; the error is discarded, and a nil
   `*logging.Logger` is safe to call. `logger.Info(EventStartup)` is emitted
   before anything else. Diagnostics can therefore be dropped while stdout
   stays reserved.
3. `config.LoadFrom(paths.Config)` — strict decode plus `Config.Validate`
   (`schemaVersion == 1`, non-placeholder client ID, `repositoryId > 0`,
   owner/repo/branch exactly `neelbangera`/`lecture-transcripts`/`main`).
   Failure logs `EventConfigLoaded` through `logFatal`.
4. `queue.AcquireLock(paths.Lock)` — `flock LOCK_EX|LOCK_NB` on
   `queue.lock`. `queue.ErrAlreadyRunning` logs `EventLockAlreadyRunning`;
   any other error logs `EventLockAcquired`; success logs
   `EventLockAcquired` and defers `lock.Release()`.
5. `queue.Open(paths.Queue)` — creates the `0700` directory and `0600`
   database, applies/migrates the schema, and defers `store.Close()`; logs
   `EventQueueOpened`.
6. `auth.NewStore()` then `auth.NewManager(credentialStore, auth.Config{...})`
   with `ClientID`, `RepositoryID`, `Owner`, `Repo`, `Branch` copied from the
   validated config. Both failures log `EventAuthState`. `NewManager` performs
   no network access; it restores persisted credential/transaction state.
7. `github.NewClient(github.ClientConfig{Owner, Repo, Branch, Credentials:
   authManager})` — failure logs `EventGitHubRequest`. Production uses the
   default `https://api.github.com` base URL.
8. `processor.New(processor.Config{...})` with `Store`, `Auth`, `Publisher`,
   `Logger`, `Version: version`, and `WriteTimestamped: &cfg.WriteTimestamped`
   (a pointer so an explicit `false` is distinguishable from an absent config
   field). Failure logs `EventStartup`.
9. `host.FormatOriginArgument(args)` — Chrome supplies the extension origin as
   `argv[1]`; missing/empty is an error logged as `EventProtocolError`.
10. `readAllowedOrigin()` — reads the installed manifest (below), requires
    exactly one origin, and validates it with `host.ValidateOrigin(allowed,
    allowed)` before returning it. Failure logs `EventProtocolError`.
11. `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)`.
12. `host.NewServer(stdin, stdout, proc, proc, origin, allowedOrigin)` and
    `server.Run(ctx)`; after it returns, `logger.Info(EventShutdown)`.
13. Shutdown classification: `nil`, `io.EOF` (Chrome closed the port), and
    `context.Canceled` (signal) are success; every other error is returned and
    `main` prints `lecture-uploader: <err>` to stderr and exits `1`.

### Origin source

`readAllowedOrigin` is the only configured source of the allowed origin:

```text
~/Library/Application Support/Google/Chrome/NativeMessagingHosts/
  com.neelbangera.lecturetranscripts.json
```

(`hostManifestName = "com.neelbangera.lecturetranscripts.json"`.) The JSON is
decoded into `struct { AllowedOrigins []string \`json:"allowed_origins"\` }`.
A read error, decode error, or any `len(AllowedOrigins) != 1` fails closed with
a wrapped error; the single value must pass `host.ValidateOrigin`, which
enforces the exact `chrome-extension://<32 chars a-p>/` shape. Wildcards,
multi-origin manifests, alternate schemes, and path variants are rejected.

### Version injection

```go
// version is injected by scripts/build-uploader.sh with
// -ldflags "-X main.version=...".  Keep the name and declaration stable.
var version = "dev"
```

The build script validates `--version` (or the `version` field in
`package.json`) as `[0-9A-Za-z._+-]`, at most 32 characters, starting
alphanumeric, then runs:

```sh
go build -trimpath -ldflags "-X main.version=$version" \
  -o "$output" ./cmd/lecture-uploader
```

`processor.DefaultVersion = "dev"` is the fallback if `processor.New` is given
an empty version. The uploader version is surfaced to the popup through
`StatusMessage.uploaderVersion`.

### stdout/stderr rules

- stdout: only 4-byte little-endian framed protocol responses, written by
  `host.WriteFrame`. The package comment states the rule explicitly.
- Logs: the sanitized rotated file at `paths.Log`; startup failures are logged
  as events with no free-form detail (`logFatal` calls
  `logger.Error(event, logging.Fields{})` and returns the error).
- stderr: only the final `lecture-uploader: %v` line from `main`, never in a
  successful session.
- No other package prints; `logging` writes only to its file.

## Data flow

```text
Chrome (persistent stdio port)
  → os.Args[1] origin + config.json + queue.lock + queue.sqlite3
  → auth.Manager (Keychain) → github.Client
  → processor.Processor (queue drain + RequestHandler + SessionLifecycle)
  → host.Server.Run(stdin, stdout)
       ReadFrame → protocol.DecodeRequest → OnConnect/HandleRequest
       → protocol.EncodeResponse → WriteFrame → stdout
```

On `connect`, `host.Server` calls `processor.OnConnect`, which recovers stale
leases, promotes due retries, begins/resumes device-flow authorization, and
starts the single drain goroutine. On port close it calls `OnDisconnect`, which
stops the drain after any in-flight job finishes. SIGINT/SIGTERM cancels the
server context, which also triggers `OnDisconnect` through the deferred call.

## File responsibilities

| File | Role | Key exports |
| --- | --- | --- |
| `main.go` | whole command package: assembly, origin read, signal handling, exit codes | `main`, `run(args, stdin, stdout)`, `readAllowedOrigin()`, `logFatal`, `version`, `hostManifestName` |
| `../ARCHITECTURE.md` | this document | — |

There are no other files in this directory; test files, helper packages, and
flags beyond the Chrome-supplied origin argument do not exist.

## Testing and verification

- `cd uploader && go build ./...` compiles the command (the required
  verification for this task).
- `go test ./...` reports `cmd/lecture-uploader [no test files]`; coverage of
  the composition comes from `internal/processor`'s
  `TestHostSessionEndToEnd`, which runs a real `host.Server` against the
  processor over in-memory pipes, and from the `internal/host` server tests
  (origin rejection, lifecycle hooks, invalid-request routing, oversized-frame
  termination).
- Build/version behavior is verified by `scripts/build-uploader.sh --dry-run`
  and `sh scripts/tests/run.sh`; see `docs/TESTING.md` and `docs/SETUP.md`.
- Manual smoke test: install the host with
  `scripts/install-native-host.sh <loaded-extension-id>`, run the binary
  directly with a valid origin argument, and confirm no stdout bytes are
  emitted before a request frame arrives.

## Related plan sections

- Stage 4, item 1 — `TECHNICAL_PLAN.md` lines 883–903 (host loop, startup
  promotion/lease recovery, drain states).
- Stage 5, item 4 — lines 905–922 (connect/reset wiring through the protocol).
- Stage 7, items 2 and 4 — lines 981–998 (build one native executable, install
  the rendered manifest, validate the Chrome-supplied origin).
- Native uploader file inventory — line 1093; build/install files — line 1129.
- Generated and machine-local artifacts — lines 1143–1163 (binary path,
  config, queue, log, host manifest).

## How to change this directory safely

1. Keep `run` as a linear assembly: new components must be constructed after
   their dependencies and before `host.NewServer`, and must fail closed before
   the first protocol byte.
2. Never move logic into `main.go`. If a behavior needs a test, it belongs in
   the package that owns it; only wiring stays here.
3. Keep `var version = "dev"` and its name/declaration stable — the build
   script's `-ldflags` target depends on it.
4. Never write to stdout directly and never print errors to stdout; only
   `main` may touch stderr.
5. If startup order changes, update `docs/SETUP.md`, `docs/TROUBLESHOOTING.md`,
   and this document, and re-run `scripts/tests/run.sh`.
6. When adding a component to the stack, verify it satisfies the narrow
   interface (`host.RequestHandler`, `host.SessionLifecycle`,
   `github.CredentialSource`, `processor.AuthManager`, or
   `processor.Publisher`) rather than widening those interfaces.
