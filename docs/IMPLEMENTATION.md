# Documentation Implementation Plan

## Goal

Make the documentation describe the repository that actually exists and give the owner enough information to install, operate, secure, test, and recover the finished system.

## Current state

- `TECHNICAL_PLAN.md` is the normative contract and its readiness section now reflects the implemented tree.
- `docs/STAGE_0_REPORT.md` says Stage 0 is code-ready with render-time measurement and machine-local provisioning outstanding.
- `README.md` documents status, prerequisites, build/test, and links to the guides.
- `SETUP.md`, `SECURITY.md`, `TESTING.md`, and `TROUBLESHOOTING.md` are in the tree.

## Work items

1. Reconcile the readiness/status sections of `TECHNICAL_PLAN.md`, `PHASE_1_DECISIONS.md`, `README.md`, and the Stage 0 report.
2. Keep all product behavior, limits, statuses, paths, and security decisions in `TECHNICAL_PLAN.md`; do not silently redefine them in a guide.
3. Add `docs/SETUP.md` covering prerequisites, build, unpacked Chrome loading, GitHub App/device flow setup, machine-local config, Native Messaging installation, and extension-ID drift.
4. Add `docs/SECURITY.md` covering permissions, Keychain storage, queue/database modes, log redaction, URL sanitization, remote-hash trust, reset, and private/public repository considerations.
5. Add `docs/TESTING.md` covering unit, fixture, offline, restart, duplicate, conflict, retry, and end-to-end checks.
6. Add `docs/TROUBLESHOOTING.md` covering host-not-found, protocol mismatch, authorization, unavailable repository, pending handoffs, queue-full notices, retries, and permanent conflicts.
7. Record the real render-time values and provisioning verification only after the owner performs those steps. Never commit client IDs, repository IDs, tokens, extension IDs, or raw authenticated captures.

## Done when

- No document calls the implemented repository “design-only.”
- A new owner can follow the setup guide without guessing a path, permission, or status meaning.
- Documentation contains no secrets or private page captures.
- Every command in the README is executable from a clean checkout.
