# Uploader Internal Packages Implementation Plan

## Goal

Provide the isolated components used by the Native Messaging host without allowing protocol, persistence, credentials, or remote API concerns to blur together.

## Dependency boundaries

`protocol` ← `host`

`config` → `queue`, `auth`, `github`, `logging`

`queue` + `auth` + `github` + `markdown` + `retry` → `processor`

`processor` + `host` → `cmd/lecture-uploader`

No package may expose transcript text in status/logging types, and no package other than `auth` may access credential storage.

## Planned packages

- `protocol`: closed wire types and validators.
- `host`: Native Messaging framing, origin validation, lifecycle.
- `config`: exact machine-local configuration loader.
- `queue`: SQLite schema/store, deduplication, leases, transitions, pagination.
- `auth`: Keychain store and GitHub Device Flow.
- `github`: HTTP client, Contents API, sanitized error mapping.
- `markdown`: deterministic lecture rendering and source-URL policy.
- `retry`: capped backoff and test clock.
- `processor`: serial drain and state transitions.
- `logging`: bounded privacy-safe structured logs.

## Package-level acceptance

Each package must have unit tests with fakes where external state is involved. The processor must be testable without a real GitHub account, Keychain, or Chrome connection. The command package must only compose tested components.

## Implementation rule

Use the field names, status vocabulary, queue schema, limits, and transition rules from `TECHNICAL_PLAN.md`; do not derive a simpler replacement from the current partial code.
