# Fixture Packet Implementation Plan

## Goal

Keep the Stage 0 evidence packet sanitized, measurable, and directly consumable by parser tests and the production build.

## Current artifacts

- `lecture-page.html`: valid rendered lecture page.
- `lecture-page.selectors.json`: observed selectors and completion/date policy.
- `lecture-page.expected.json`: expected valid parser result.
- `overview-page.html`: linked overview used for exact player-link/date correlation.
- `no-number-lecture-page.html` and expected output: fail-closed identity case.
- `loading-transcript-page.html` and `non-lecture-page.html`: negative cases.
- `course-mapping.json`: current explicit allowlist.
- `transcript-size-report.json`: byte-cap evidence; render-time values are still null.

## Work items

1. Run the documented render-time snippet on both permitted samples.
2. Replace both `renderTimeMs: null` values with measured values.
3. Recompute `observedMaxima.renderTimeMs` and confirm every maximum is below its approved limit.
4. Add a fixture validation test that checks required fields, exact selector policy, supported term format, hash format, and size-cap proof.
5. Keep fixture HTML sanitized: no user identifiers, cookies, media URLs, session values, or real route IDs.
6. Preserve the rule that the overview badge is not a lecture number.
7. Do not add courses or page shapes without new verified evidence and corresponding plan/decision updates.

## Do not do

- Do not read local raw captures at runtime.
- Do not use fixture prose as a substitute for observed values.
- Do not replace an ambiguous lecture number with the category badge.
- Do not raise caps just to make a sample pass.

## Done when

The packet is complete, all values are observed or explicitly verified, parser tests pass against it, and the build can embed only the required selector/config data.
