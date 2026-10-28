# Repository Implementation Plan

Current work: easier installation and distribution. Approved 2026-10-04; implementation has not started.

Read [TECHNICAL_PLAN.md](TECHNICAL_PLAN.md#easier-installation-and-distribution) for the requirements and [docs/DISTRIBUTION_PLAN.md](docs/DISTRIBUTION_PLAN.md) for the handoff. The technical plan remains authoritative. Read each affected directory's architecture document before changing its code; older directory task plans describe the original implementation and must not override the new distribution phase.

## Current baseline

- The extension, macOS uploader, Native Messaging protocol, durable queue, Keychain auth, publishing, and source build/install scripts exist.
- Setup currently requires developer tools, a checkout, fixed-repository source edits, a user-created GitHub App, local JSON, and a copied extension ID.
- No release installer or published extension identity is claimed by this plan.
- Existing Stage 0 live evidence and real-machine provisioning checks still apply. Keep unrelated work intact.

## Execution order

1. **Resolve discovery.** Verify a private-repository setup flow that discovers the numeric ID and produces a repository-restricted final credential. Record the API sequence, cleanup behavior, and any second authorization before implementing the wizard.
2. **Generalize local configuration.** Accept a validated user destination while preserving the config schema, initialized `main`, target verification, and strict failure behavior.
3. **Add guided setup.** Implement uploader `--setup`, public App metadata input, authorization, safe config persistence, and protection against redirecting existing queue/outbox state. Keep human-readable CLI output outside Native Messaging.
4. **Package and install.** Build matching prebuilt assets, install to stable per-user locations, adapt host install/uninstall, and add upgrade/rollback checks. Preserve all durable state by default.
5. **Prepare releases.** Add a tagged draft-release workflow, checksums, metadata, and a maintainer runbook. Provision the real public App before a functional release is published.
6. **Verify and document the release.** Perform the required implementation checks and real Mac/Chrome acceptance. Replace source-first user instructions only when the release artifacts work.
7. **Finish published distribution.** Use a verified Chrome Web Store identity and signed/notarized Mac installer for the later three-step installation flow.

## Constraints

- New source paths must appear in the technical plan's next-phase inventory before being added.
- Preserve capture behavior, byte limits, hash/protocol contracts, queue ownership, and write-once publishing.
- Keep one account and repository per installation. No hosted service or automatic updater is part of this work.
- Never distribute tokens, client secrets, App private keys, or signing credentials. Verified public App/extension metadata is permitted.
- Do not fabricate external provisioning, live page measurements, store identity, or signing evidence.

## Done when

Each milestone meets the gates in [the distribution guide](docs/DISTRIBUTION_PLAN.md#checks-required-before-release). In particular, D2 must work on a clean supported Mac without developer tools, source edits, manual JSON, or creating a GitHub App, and updates must preserve queued transcripts and connection state. D3 adds verified store installation and signed/notarized Mac distribution. Planned work must stay labeled as planned until its checks have passed.
