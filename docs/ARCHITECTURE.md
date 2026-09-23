# Documentation Architecture

## Purpose

`docs/` is the owner-facing explanation layer for the installed system. It maps
the questions an owner or agent asks — how to install, what the security
boundary is, what to run, what a failure means — to the one guide that answers
each. The guides explain the contracts; they never define them.

## Boundaries and dependencies

- Authority flows downward:

  ```text
  TECHNICAL_PLAN.md        (normative: behavior, limits, statuses, schemas)
    > PHASE_1_DECISIONS.md (rationale; must agree with the plan)
      > docs/* guides      (explanation and procedure)
  ```

  `TECHNICAL_IMPLEMENTATION_PROPOSAL.md` is historical and not an authority.
  When a guide and the plan disagree, the plan wins and the guide is corrected.
- `docs/` may link to and quote contracts, but it must not invent a status
  name, path, limit, permission, or protocol field. New behavior is recorded
  in `TECHNICAL_PLAN.md` before it appears here.
- `docs/render-time-snippet.js` is the one executable artifact in this
  directory: a DevTools console snippet for the live Stage 0 render-time
  measurement. It is not loaded by the extension.
- Every guide must remain free of secrets and raw captures. Real values —
  GitHub App client ID, numeric repository ID, access/refresh token, loaded
  extension ID, session URL, cookie — never appear. Use placeholders such as
  `<loaded-extension-id>`, `<github-app-client-id>`, and
  `<numeric-repository-id>`.

## Contracts and invariants

- **Documentation map.** Each question has exactly one primary guide:

  | Question | Guide |
  | --- | --- |
  | What is this project and what is its current status? | [`../README.md`](../README.md) |
  | How do I build, load, configure, authorize, install, and verify it? | [`SETUP.md`](SETUP.md) |
  | What is the security boundary: permissions, Keychain, queue/log modes, redaction, URL sanitization, conflicts, resets? | [`SECURITY.md`](SECURITY.md) |
  | What test layers exist, what do they cover, and what must be checked by hand on the real machine? | [`TESTING.md`](TESTING.md) |
  | A specific symptom is happening; how do I recover? | [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) |
  | What was the Stage 0 discovery evidence and what is still outstanding? | [`STAGE_0_REPORT.md`](STAGE_0_REPORT.md) |
  | What raw evidence led to the Stage 0 decisions? | [`STAGE_0_LECCAP_OBSERVATIONS.md`](STAGE_0_LECCAP_OBSERVATIONS.md) |
  | How do I measure render time on a real page? | [`render-time-snippet.js`](render-time-snippet.js) and the recipe in `STAGE_0_REPORT.md` |
  | How is the documentation tree organized and what outranks what? | this file |

- **Secrets and captures.** No committed document may contain a real client ID,
  repository ID, token, device code, cookie, session value, loaded extension
  ID, raw authenticated page capture, or rendered host manifest. Fixtures are
  sanitized (`extension-tests/fixtures/`); raw captures are gitignored at the
  repository root.
- **Placeholders.** Values the owner supplies are written as `<...>` and are
  explicitly marked as not-to-be-committed. A guide that needs an example uses
  a visibly nonfunctional placeholder.
- **Status names.** `docs/` uses the exact machine statuses and user labels
  from `TECHNICAL_PLAN.md` § Canonical status vocabulary and
  `extension/src/status.ts`; it does not translate or rename them.
- **Paths and commands.** Paths and commands in the guides are executable from
  a clean checkout; `README.md` and `docs/IMPLEMENTATION.md` require this.

## Data flow

Documentation is produced and consumed by hand:

```text
TECHNICAL_PLAN.md / PHASE_1_DECISIONS.md / implementation
  -> docs/* guides -> owner performing setup, verification, or recovery
  -> STAGE_0_REPORT.md + transcript-size-report.json <- live measurement results
```

The one write-back path is Stage 0: the owner runs
`docs/render-time-snippet.js` on real permitted pages and records the measured
milliseconds in `extension-tests/fixtures/transcript-size-report.json` (both
`renderTimeMs` fields plus `observedMaxima`), as described in `STAGE_0_REPORT.md`
and `docs/TESTING.md`. No other document is generated from runtime data.

## File responsibilities

| File | Role |
| --- | --- |
| `SETUP.md` | Complete install runbook: prerequisites, build/test commands, unpacked loading and ID capture, extension settings, GitHub App and repository provisioning, machine-local config shape, uploader build, host installation, authorization, verification, extension-ID drift, uninstall/reset |
| `SECURITY.md` | Implemented security boundary: manifest permissions, no credentials in the extension, Keychain records and device-flow storage, queue data and file modes, log redaction and rotation, source-URL sanitization, remote-hash trust, write-once conflicts, reset semantics, private/public repository consequences, secret rotation |
| `TESTING.md` | Test layers and commands, suite coverage tables, fixture packet inventory, offline/restart/duplicate/conflict/retry checks, fake-versus-real boundary, packaging self-tests, owner-only real-machine checks |
| `TROUBLESHOOTING.md` | Symptom-to-fix guide for host-not-found, protocol mismatch, authorization, target repository, pending handoffs, queue-full, alarm-delayed retries, log reading, permanent conflicts, rejected metadata, extension-ID drift |
| `STAGE_0_REPORT.md` | Stage 0 gate evidence: packet item status, measured size summary, recorded plan revisions, and the two outstanding live items (render-time measurement, machine-local provisioning) |
| `STAGE_0_LECCAP_OBSERVATIONS.md` | Raw evidence trail from the authenticated Leccap pages (DOM shape, selectors, timestamps, loading/completion findings) resolved into the Stage 0 packet |
| `render-time-snippet.js` | DevTools console snippet measuring activation to second stable snapshot (1500 ms debounce, 30 s timeout); the live measurement recipe |
| `IMPLEMENTATION.md` | Directory work plan for the documentation goal and its "done when" criteria |
| `ARCHITECTURE.md` | This document |

## Testing and verification

Documentation has no automated claims checker; verification is manual plus
whatever the code suites already enforce:

- `README.md` and `docs/IMPLEMENTATION.md` require every command to be
  executable from a clean checkout. The commands currently documented are
  `npm ci`, `npm run typecheck`, `npm test`, `npm run build`,
  `cd uploader && go test ./...`, `sh scripts/tests/run.sh`,
  `scripts/build-uploader.sh`, and
  `scripts/install-native-host.sh <loaded-extension-id>`.
- `extension-tests/fixture-packet.test.ts` enforces that the committed fixtures
  referenced by `TESTING.md` remain sanitized: no media URLs, cookies,
  sessions, tokens, user identifiers, or unsanitized route IDs.
- `scripts/tests/run.sh` asserts the host template and rendered manifest
  properties that `SETUP.md` and `TROUBLESHOOTING.md` describe.
- The real-machine checks in `docs/TESTING.md` are the acceptance record for
  the live items; they cannot be run from a clean checkout.

Verified while writing this document: `sh scripts/tests/run.sh` passes
(`59 passed, 0 failed`), `sh -n` is clean for the four shell scripts, and
`go test ./...` passes in the uploader. The npm suites and the live
render-time measurement were not run here.

## Related plan sections

- § Implementation readiness and § Required Stage 0 packet (the evidence the
  guides describe)
- § Required provisioning packet (the machine-local config shape documented in
  `SETUP.md`)
- § Phase 1 guardrails
- § Canonical status vocabulary (the labels used throughout the guides)
- § Stage 0, § Stage 7, and § Stage 8 (evidence, packaging/documentation, and
  end-to-end verification)
- § Complete implementation file inventory § Documentation files
- § Definition of done (documentation criteria)

## How to change this directory safely

- Change the contract first: edit `TECHNICAL_PLAN.md`, then the decision log if
  the rationale changed, then the guide. Never let a guide redefine behavior.
- Keep the documentation map current when a guide is added, removed, or
  repurposed; update this file and the documentation list in `README.md`.
- Never add a real client ID, repository ID, token, device code, extension ID,
  session URL, or raw capture. Keep placeholders in `<...>` form.
- Keep status names, paths, limits, and commands copied from the plan; when a
  command or path changes in the tree, update `README.md`, `SETUP.md`,
  `TESTING.md`, and `TROUBLESHOOTING.md` in the same change.
- After editing, re-run the commands in "Testing and verification" that the
  edit references.

### Open questions and known inconsistencies

- `TESTING.md` is stale in this tree: it reports "7 files / 71 tests" and
  "all 9 uploader packages", while the tree has 16 Vitest suites and 10 Go
  internal packages; its coverage-boundary paragraph says the outbox and
  background paths have no dedicated suites although `background.test.ts` and
  `extension-storage.test.ts` exist; its fixture table says the course mapping
  is only `eecs484 / 2026-fall` although `course-mapping.json` also contains
  `eecs491 / 2026-fall`.
- `TROUBLESHOOTING.md` still describes lecture files as
  `<slug>/lectures/<NNN>.md`, which contradicts the plan's identity/Stage 6
  path rule, `docs/SETUP.md`, and the Go path derivation
  (`uploader/internal/queue/store.go`, `<slug>/<NNN>.md`).
- `README.md` points to `docs/SETUP.md` for "the current executable gap";
  `SETUP.md` states the executable is implemented, and the only outstanding
  items are the render-time measurement and machine-local provisioning.
- `docs/IMPLEMENTATION.md` still lists creating `SETUP.md` and the other
  guides as work items even though they are present.
- No document records a link checker or claims linter, so drift between guides
  and the plan is caught only by review.
