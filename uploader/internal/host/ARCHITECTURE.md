# internal/host Architecture

## Purpose

`internal/host` is the Chrome Native Messaging transport. It turns the raw
stdio byte stream into validated `protocol.Request` values, enforces the
application frame cap and the exact-origin allowlist, drives the session
lifecycle hooks, and writes only validated protocol responses. It knows
nothing about SQLite, Keychain, GitHub, or logging; the processor is injected
as two narrow interfaces.

- File: `uploader/internal/host/native_messaging.go`.
- Consumers: `uploader/cmd/lecture-uploader` (constructs and runs the server)
  and `uploader/internal/processor` (implements both injected interfaces).

## Boundaries and dependencies

- Imports: stdlib (`context`, `encoding/binary`, `errors`, `fmt`, `io`,
  `regexp`, `sync`) and `internal/protocol`. No other internal package.
- `RequestHandler` and `SessionLifecycle` keep the transport independent of
  queue/auth/GitHub:

  ```go
  type RequestHandler interface {
      HandleRequest(context.Context, protocol.Request) (any, error)
  }
  type SessionLifecycle interface {
      OnConnect(context.Context) error
      OnDisconnect()
  }
  ```

- `Server` holds `Reader`, `Writer`, `Handler`, `Lifecycle`, `Origin`, and
  `AllowedOrigin`; `NewServer` is a plain constructor. `Origin` comes from
  Chrome's `argv[1]`; `AllowedOrigin` is read by `cmd` from the installed
  manifest. `host` itself never reads the filesystem or environment.
- The handler may return any value that `protocol.EncodeResponse` accepts; the
  host is the enforcement point that rejects anything else.

## Contracts and invariants

### Framing

Chrome Native Messaging frames are a 4-byte little-endian unsigned length
followed immediately by exactly that many payload bytes; there is no
terminator or alignment.

- `ReadFrame(reader io.Reader) ([]byte, error)`:
  - `io.ReadFull` on the 4-byte header. A clean EOF with zero bytes read
    returns `io.EOF`; a partial header returns
    `&FrameError{Kind: ErrMalformedFrame, Cause: io.ErrUnexpectedEOF}`.
  - `claimedLength := binary.LittleEndian.Uint32(header[:])`.
  - If `uint64(claimedLength) > uint64(MaxFrameBytes)` it returns
    `&FrameError{Kind: ErrFrameTooLarge, ClaimedLength: claimedLength}`
    **before allocating or reading the body**.
  - Otherwise it allocates exactly `claimedLength` bytes and reads the body;
    a short body returns `&FrameError{Kind: ErrMalformedFrame,
    ClaimedLength: claimedLength, Cause: io.ErrUnexpectedEOF}`.
  - A zero-length frame is a legal frame at this layer; it is rejected by
    `protocol.DecodeRequest` (not a JSON object), not by framing.
- `WriteFrame(writer io.Writer, payload []byte) error`:
  - Rejects `len(payload) > MaxFrameBytes` with
    `&FrameError{Kind: ErrFrameTooLarge, ClaimedLength: uint32(len(payload))}`
    — the uploader never truncates or chunks a logical protocol message.
  - Writes the LE length, then the payload through `writeAll`, which loops on
    short writes and returns `io.ErrShortWrite` for a nonsensical write
    result.
- `FrameError` implements `Error()` and `Unwrap() error` returning `Kind`, so
  `errors.Is(err, ErrFrameTooLarge)` works.

### 1 MiB application cap

`MaxFrameBytes = protocol.MaxFrameBytes = 1 << 20` (1,048,576 bytes),
excluding the 4-byte prefix. The cap applies in **both** directions and is a
conservative product cap, not a claim about Chrome's own limits. Jobs are
additionally bounded by `protocol.MaxSerializedJobByte` (972,800 bytes) so a
valid job always fits under the frame cap with envelope headroom.

### Origin validation

The only accepted origin shape is
`chrome-extension://<32 characters a-p>/`:

- `extensionIDPattern = ^[a-p]{32}$` (`native_messaging.go:296`).
- `ExtensionOrigin(extensionID)` builds that string or returns
  `ErrOriginMismatch`.
- `ValidateOrigin(origin, allowedOrigin)` is an exact allowlist check:
  - empty `allowedOrigin` → `ErrOriginNotConfigured`;
  - both values must pass `validateOriginShape`, which rejects `""` and `"*"`,
    requires the `chrome-extension://` prefix and a trailing `/`, and requires
    exactly the 32-character `a-p` ID in between;
  - `origin != allowedOrigin` → `ErrOriginMismatch`.
  Wildcards, alternate schemes, path variations, different IDs, and a missing
  configuration all fail closed.
- `FormatOriginArgument(args)` returns `args[0]` (Chrome passes the extension
  origin as the first native-host argument) or an error when it is missing or
  empty.
- The **installed Chrome manifest is the configured source of the allowed
  origin**, but it is read in `cmd.readAllowedOrigin`, not here:
  `~/Library/Application Support/Google/Chrome/NativeMessagingHosts/
  com.neelbangera.lecturetranscripts.json` must contain exactly one
  `allowed_origins` entry, which `cmd` re-validates with `ValidateOrigin`
  before passing it to `NewServer`. The installer
  (`scripts/install-native-host.sh`) renders exactly one origin with mode
  `0600` and never a wildcard.
- `Server.Run` validates the origin **before reading any frame**
  (`TestServerRunRejectsOriginBeforeReading`), so a wrong or missing origin
  cannot consume a request.

### Persistent port lifecycle

`Server.Run(ctx)` owns one persistent Chrome port:

1. Validate origin.
2. Require non-nil `Reader`, `Writer`, and `Handler`; `Lifecycle` is optional.
   Missing transport pieces return `ErrInvalidHandler`.
3. `defer s.Lifecycle.OnDisconnect()` when a lifecycle is present.
4. Loop until the context is canceled or stdin ends:
   - `ctx.Done()` → return `ctx.Err()`;
   - `ReadFrame` error → (oversized handling below) return the error;
   - `DecodeRequest` error → write a validation response, continue;
   - `connect` + lifecycle → `OnConnect(ctx)`; on failure write a safe error
     frame and continue without calling the handler;
   - `Handler.HandleRequest` → `protocol.EncodeResponse` → `WriteFrame`.
5. On clean port close `ReadFrame` returns `io.EOF`, which propagates to
   `cmd`, where it is treated as a normal shutdown.

Requests are handled serially on the stdio loop (no per-request goroutines);
`writeMu` guards `WriteFrame` so any future concurrent writer cannot interleave
frames. The process stays alive for the port and exits when it closes; there is
no idle timeout.

### Connect/disconnect hooks

- `SessionLifecycle.OnConnect` is called exactly when a validated
  `protocol.RequestConnect` arrives, before the handler answers it.
- `OnDisconnect` is called exactly once per `Run` via `defer`, installed only
  after the origin and transport checks pass. A refused startup (bad origin,
  missing handler) never calls it; every session that reaches the read loop
  does, including oversized-frame termination and write failures.
- `processor.Processor` implements both hooks: `OnConnect` recovers stale
  leases, promotes due retries, begins/resumes device-flow authorization, and
  starts the single drain goroutine; `OnDisconnect` stops the drain after any
  in-flight job finishes and releases the session channels. Authorization
  failures are reported through status, not as a connect error, so `OnConnect`
  normally returns nil.

### Oversized-frame termination

A claimed frame over the cap is fatal to the stdio session by design: the body
cannot be safely resynchronized without trusting an unbounded attacker-
controlled length. `Server.Run` therefore:

1. writes exactly one bounded error frame with `requestId = nil`,
   `category = protocol.ErrorRejectedOversized`, `retryable = false`; then
2. returns the `ErrFrameTooLarge` error, ending the session.

It never attempts to skip the claimed body or continue reading. Malformed
(truncated) frames return an error without a diagnostic frame because the
stream position is unknowable.

### Error responses

- `safeError(err)` maps a `protocol.ProtocolError` with a known category to
  `(category, retryable)`; every other error becomes
  `(protocol.ErrorInternal, false)`. Raw error text never crosses the wire.
- `writeError(requestID, category, retryable)` builds a
  `protocol.ErrorMessage{Type: "error", ProtocolVersion: 1, RequestID,
  Category, Retryable}` and validates it through `protocol.EncodeResponse`
  before framing.
- Invalid requests are answered by `writeValidationFailure`: for
  `submit_job`, the rejection categories
  (`rejected_invalid_schema`, `rejected_unknown_field`, `rejected_oversized`,
  `rejected_invalid_hash`, `rejected_unsafe_url`, `rejected_queue_full`)
  become an `ack` with `operation: "submit"` and the category as `status`
  (`rejectedAck`), echoing a valid `lectureKey`/`contentHash` when the
  partial decode retained them; every other failure becomes an `error` frame
  with the request's `requestId` when one was decoded.
- `requestIDPointer` omits `requestId` (`nil`) when the request had none.
- If `protocol.EncodeResponse` rejects a handler value, the host emits an
  internal error frame instead of writing an unvalidated payload.
- A write failure returns the write error and ends the session
  (`TestServerRunDisconnectsOnWriteFailure`).

## Data flow

```text
Chrome stdin
  → ReadFrame (LE uint32 length, ≤1 MiB)
  → protocol.DecodeRequest (strict JSON)
  → [connect] Lifecycle.OnConnect → processor: lease recovery, retry
     promotion, auth Begin, start drain
  → Handler.HandleRequest (processor: enqueue / status / retry / discard /
     reset)
  → protocol.EncodeResponse (closed vocabulary validation)
  → WriteFrame (LE uint32 length + payload)
  → Chrome stdout
  → on exit: Lifecycle.OnDisconnect → processor stops the drain after the
     in-flight job
```

Diagnostics never use stdout; the host writes no logs itself.

## File responsibilities

| File | Role | Key exports |
| --- | --- | --- |
| `native_messaging.go` | framing, origin validation, server loop, lifecycle hooks, error responses | `MaxFrameBytes`, `FrameError`, `ReadFrame`, `WriteFrame`, `RequestHandler`, `SessionLifecycle`, `Server`, `NewServer`, `(*Server).Run`, `ExtensionOrigin`, `ValidateOrigin`, `FormatOriginArgument`, `ErrFrameTooLarge`, `ErrMalformedFrame`, `ErrOriginNotConfigured`, `ErrOriginMismatch`, `ErrInvalidHandler` |
| `native_messaging_test.go` | transport unit tests with in-memory pipes and recording fakes | `TestReadFrame*`, `TestWriteFrame*`, `TestValidateOrigin`, `TestServerRun*` |
| `IMPLEMENTATION.md` | work items and done criteria for this package | — |
| `ARCHITECTURE.md` | this document | — |

## Testing and verification

`go test ./internal/host` covers, offline:

- **Framing**: little-endian length, short header reads, clean EOF, truncated
  header/body, zero-length frames, the exact-limit boundary, oversized claimed
  lengths, emitted oversized payloads, short writes, and write errors
  (`TestReadFrameLittleEndianLength`, `TestReadFrameShortReads`,
  `TestReadFrameCleanEOF`, `TestReadFrameTruncated`,
  `TestReadFrameZeroLength`, `TestReadFrameOversizedClaimedLength`,
  `TestReadFrameExactLimit`, `TestWriteFrameLittleEndianLength`,
  `TestWriteFrameShortWrites`, `TestWriteFrameErrors`).
- **Origins**: `ExtensionOrigin` shape, `ValidateOrigin` exact matches and
  missing/wildcard/mismatched/malformed origins, `FormatOriginArgument`
  (`TestExtensionOrigin`, `TestValidateOrigin`, `TestFormatOriginArgument`).
- **Server**: request/lifecycle counting, invalid requests never reaching the
  handler, submit rejections becoming acks, oversized frames terminating the
  session, handler errors becoming safe frames, request serialization,
  `OnConnect` failure handling, origin rejection before reading, missing
  configuration, missing handler, context cancellation, write-failure
  disconnect, and stdout containing only framed protocol data
  (`TestServerRun*`).
- The host ↔ processor seam is additionally exercised by
  `processor.TestHostSessionEndToEnd`.

## Related plan sections

- Canonical Native Messaging contract — `TECHNICAL_PLAN.md` lines 287–390
  (envelopes, correlation, pagination, drain lifecycle, version fields).
- Canonical Native Messaging host template — lines 644–660 (single allowed
  origin, `{{BINARY_PATH}}`/`{{EXTENSION_ID}}` substitutions).
- Stage 4, item 1 — lines 883–903 (1 MiB cap in either direction, persistent
  port, origin validation, malformed/oversized handling before parsing).
- Stage 7, item 4 — lines 981–998 (host validates the Chrome-supplied origin
  and exits on mismatch).
- Native uploader file inventory — line 1097.

## How to change this directory safely

1. Keep framing at this layer: no JSON parsing, no logging, no queue access.
   New request semantics belong in `protocol` or `processor`.
2. Never raise `MaxFrameBytes` locally; it must stay equal to
   `protocol.MaxFrameBytes` so the extension and uploader agree on the cap.
3. Preserve the oversized-frame policy: one bounded error frame, then
   terminate. Do not add resynchronization or body-skipping.
4. Keep origin validation exact-match; do not add wildcard, suffix, or
   prefix matching, and do not move manifest reading into this package.
5. Keep responses flowing through `protocol.EncodeResponse`; never frame a
   handler value directly.
6. Keep `OnDisconnect` deferred exactly once and `OnConnect` tied to
   `RequestConnect`; lifecycle side effects must stay in the processor.
7. Add tests for every new error path with `bytes.Buffer`/`io.Pipe` fakes and
   run `go test ./internal/host` plus `go test ./...`.
