# Uploader Implementation Plan

## Goal

Build the macOS local uploader that owns durable queue state, GitHub authentication, retries, and write-once publishing.

## Current state

- `go.mod` declares Go 1.24, SQLite, and text dependencies.
- Only `internal/host` and `internal/protocol` exist.
- There is no executable, queue, config, auth, GitHub, Markdown, retry, processor, logging, or testdata implementation.

## Package order

Implement in this order:

1. `internal/protocol` tests and any contract fixes.
2. `internal/host` framing/origin tests.
3. `internal/config` for machine-local target validation.
4. `internal/queue` for durable state and pagination.
5. `internal/markdown` for deterministic file output.
6. `internal/github` for authenticated Contents API operations.
7. `internal/retry` for persistent backoff.
8. `internal/auth` for Keychain and Device Flow.
9. `internal/processor` to connect queue, auth, GitHub, rendering, and retry.
10. `cmd/lecture-uploader` to assemble the host process.

The queue and processor must be testable with fake auth and GitHub implementations before macOS Keychain integration is attempted.

## Required behavior

- Validate every request before persistence.
- Enqueue at least once and deduplicate by `lectureKey` plus `contentHash`.
- Persist acknowledged jobs and retry state in SQLite.
- Process jobs serially with leases and restart recovery.
- Use GitHub App Device Flow and macOS Keychain; never use a plaintext token file.
- Write only the configured repository/branch and only machine-owned lecture paths.
- Treat same-hash remote content as unchanged and different/malformed content as conflict.
- Emit bounded, privacy-safe status summaries.

## Build requirements

- Add `cmd/lecture-uploader/main.go`.
- Add a macOS build script with cgo enabled.
- Fail closed when not running on the supported macOS/toolchain target.
- Keep the executable outside version control through `.gitignore`.

## Done when

`go test ./...` has meaningful coverage for every package, the executable can serve one Native Messaging session, and a fake GitHub integration can enqueue, process, retry, deduplicate, and conflict safely.
