# Native Messaging Host Implementation Plan

## Goal

Provide a conservative, long-lived Chrome Native Messaging transport for the uploader processor.

## Current state

`native_messaging.go` already implements frame reads/writes, the one-megabyte cap, origin validation, a persistent server loop, lifecycle hooks, and bounded error responses. It has no tests and no real request handler yet.

## Work items

1. Add tests for:
   - little-endian frame encoding;
   - short reads and short writes;
   - clean EOF;
   - malformed/truncated frames;
   - oversized claimed and emitted frames;
   - exact origin match;
   - missing, wildcard, and mismatched origins.
2. Test that invalid requests produce safe validation responses and do not reach the handler.
3. Test that oversized frames terminate the session rather than attempting resynchronization.
4. Test connect/disconnect lifecycle calls and request serialization.
5. Connect `Server` to the processor through a handler that returns only `protocol.EncodeResponse`-validated messages.
6. Ensure stdout contains only framed protocol data; diagnostics must use sanitized logging on stderr/files as defined by the plan.

## Done when

The host transport is fully tested independently of SQLite, GitHub, Keychain, and Chrome, and the command package can inject a real processor through the existing interfaces.
