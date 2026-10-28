# Documentation Implementation Plan

Current work: documentation for easier installation. The distribution phase is planned; existing source setup remains available.

## Entry points

- [../TECHNICAL_PLAN.md](../TECHNICAL_PLAN.md#easier-installation-and-distribution): authoritative distribution requirements and file inventory.
- [DISTRIBUTION_PLAN.md](DISTRIBUTION_PLAN.md): setup flow, milestones, release inputs, lifecycle, and acceptance checks.
- [../IMPLEMENTATION.md](../IMPLEMENTATION.md): implementation execution order.
- [SETUP.md](SETUP.md): current source-installation instructions.

## Documentation to deliver with implementation

| Milestone | Documentation work |
| --- | --- |
| D1 | Record the verified private-repository discovery/authorization sequence; explain general config validation and safe destination changes |
| D2 | Add `RELEASING.md` with exact packaging, artifact verification, draft/publish, rollback, and clean-machine evidence; update SETUP with the shipped guided installer |
| D2 | Update README to the actual download/install/connect flow; move developer prerequisites to the source-building section |
| D2 | Update SECURITY, TESTING, and TROUBLESHOOTING for the public App, stable paths, installer failures, queue preservation, and explicit reset |
| D3 | Document the real store identity, signed/notarized Mac package, and supported install/update/uninstall flow |

Keep the user guide short and factual. It must describe Lecture Transcripts for general users and link to detailed troubleshooting only where needed. Do not claim downloadable artifacts, a public App, or store/signing approval exists until verified.

## Done when

- A user can follow the instructions for a published release without guessing values or editing source/config manually.
- Every operational command describes shipped functionality; planned commands are confined to clearly marked planning documents.
- Guide behavior agrees with the technical plan and actual release checks.
- Public product metadata is distinguished from user-specific IDs and secrets. No raw captures or credentials enter the documentation.
