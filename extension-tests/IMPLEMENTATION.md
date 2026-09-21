# Extension Test Implementation Plan

## Goal

Make the browser-side test suite runnable and broad enough to protect the capture/outbox contract.

## Current state

- Parser, normalizer, job, and Native Messaging tests exist.
- The parser suite cannot be collected because `jsdom` is missing.
- Node typings are missing from the TypeScript environment.
- There are no dedicated content, background, storage, or popup tests.

## Work items

1. Add `jsdom` and `@types/node` as explicit development dependencies and update the lockfile.
2. Keep Vitest’s Node environment for pure logic; use per-test JSDOM instances for DOM behavior.
3. Add `content.test.ts` with fake timers and fake handoff dependencies for:
   - no job on ordinary page visit;
   - click activation;
   - already-expanded activation;
   - URL-change activation only when already open/populated;
   - transcript mutation restarting stability;
   - timeout and parser rejection statuses.
4. Add `extension-storage.test.ts` with an in-memory storage area for capacity, deduplication, serialization, notices, and restart reads.
5. Add `background.test.ts` with fake runtime, alarms, Native Messaging client, and storage for replay and acknowledgement behavior.
6. Add popup/status validation tests only for pure rendering/label logic; do not require a real Chrome window.
7. Add a test that the built bundle contains all manifest targets once the build script exists.

## Test discipline

- Use committed fixtures for page shape and expected output.
- Do not use raw authenticated captures.
- Assert exact status/category strings from the protocol.
- Prefer deterministic clocks and request IDs.
- Add regression tests before changing parser or queue semantics.

## Done when

`npm test` collects every suite, all current tests pass, and the suite covers both the normal capture path and every bounded failure path.
