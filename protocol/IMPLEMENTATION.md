# Shared Protocol Implementation Plan

## Goal

Keep the TypeScript extension, Go uploader, and machine-readable schemas on one exact wire contract.

## Current state

- `native-messaging.schema.json`, `transcript-job.schema.json`, normalization vectors, and source-URL vectors exist.
- TypeScript and Go each contain their own runtime validators.
- Cross-language conformance tests are missing.

## Work items

1. Review schemas against the exact request/response vocabulary in `TECHNICAL_PLAN.md`.
2. Add tests for every request type: connect, submit, status, retry, discard, and reset.
3. Add tests for every response type and every queue/auth/error status.
4. Run the normalization and source-URL vectors from both TypeScript and Go.
5. Confirm canonical job field order and UTF-8 byte measurements match across languages.
6. Confirm unknown fields, duplicate JSON keys, invalid hashes, unsafe URLs, invalid dates, and oversized payloads fail closed.
7. Treat `TECHNICAL_PLAN.md` as the authority when the schema, TypeScript, and Go disagree; resolve the discrepancy deliberately before implementation continues.

## Done when

A protocol change cannot pass in one language while failing in the other, and no response can leak raw exception text or transcript content.
