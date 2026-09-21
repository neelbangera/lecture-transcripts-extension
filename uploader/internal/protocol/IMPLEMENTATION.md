# Go Protocol Implementation Plan

## Goal

Make the Go protocol implementation an exact, defensive counterpart to the TypeScript Native Messaging client and JSON schemas.

## Current state

- `job.go` defines the canonical `TranscriptJob` and compact JSON serialization.
- `messages.go` defines request, acknowledgement, command, status, auth, queue, and error vocabularies.
- `validate.go` performs strict decoding, duplicate-key rejection, field/size checks, URL canonicalization, job validation, and response validation.
- There are currently no Go tests.

## Work items

1. Add table-driven tests for every request type and required/optional field combination.
2. Test unknown fields, missing fields, duplicate keys, trailing JSON, wrong numeric types, invalid UTF-8, and oversized frames/messages.
3. Test lecture-key consistency, course/term/lecture bounds, calendar dates, UTC timestamps, minimum transcript content, hash format, and serialized-job limits.
4. Test source URL canonicalization against `protocol/source-url-vectors.json`.
5. Test response validation for acknowledgements, command results, status pages, auth fields, counts, job summaries, and error messages.
6. Add a cross-language fixture test using a canonical job produced by TypeScript.
7. Keep all errors category-only; never include raw JSON, transcript text, remote bodies, or credentials in `Error()` output.

## Done when

`go test ./uploader/internal/protocol` covers the complete contract and agrees with the schema, vectors, and TypeScript runtime validators.
