---
name: Lecture Transcripts
description: Instrument-panel UI for a zero-loss Leccap transcript capture pipeline — flat, dense, utilitarian.
colors:
  signal-blue: "#1f6feb"
  signal-blue-dark: "#2270e6"
  on-signal: "#ffffff"
  cool-chassis: "#f5f7fa"
  cool-chassis-dark: "#12161d"
  ink-readout: "#172033"
  ink-readout-dark: "#e6eaf2"
  slate-mute: "#687286"
  slate-mute-dark: "#9aa7bb"
  panel-white: "#ffffff"
  panel-white-dark: "#1a2029"
  hairline: "#dfe4ec"
  hairline-dark: "#2a3340"
  chip-fill: "#e6eaf0"
  chip-fill-dark: "#232b36"
  chip-ink: "#4f5d72"
  chip-ink-dark: "#b6c2d4"
  quiet-fill: "#e9edf3"
  quiet-fill-dark: "#242c37"
  quiet-ink: "#26344c"
  quiet-ink-dark: "#d5dce8"
  inset-wash: "#f7f9fc"
  inset-wash-dark: "#1f2731"
  caution-amber: "#6b4b00"
  caution-amber-dark: "#e3b341"
  verified-green: "#186b31"
  verified-green-dark: "#3fb950"
  redline: "#c2261f"
  redline-dark: "#ff7b72"
  redline-wash: "rgba(194, 38, 31, 0.08)"
  redline-wash-dark: "rgba(248, 81, 73, 0.15)"
  success-wash: "rgba(26, 127, 55, 0.08)"
  success-wash-dark: "rgba(63, 185, 80, 0.15)"
  warn-wash: "rgba(107, 75, 0, 0.08)"
  warn-wash-dark: "rgba(227, 179, 65, 0.15)"
typography:
  headline:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "18px"
    fontWeight: 700
    lineHeight: 1.25
  headline-page:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "22px"
    fontWeight: 700
    lineHeight: 1.25
  title:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "14px"
    fontWeight: 700
    lineHeight: 1.3
  title-options:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "15px"
    fontWeight: 700
    lineHeight: 1.3
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "12px"
    fontWeight: 400
  body-lg:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "13px"
    fontWeight: 400
  label:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "11px"
    fontWeight: 600
  meta:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "11px"
    fontWeight: 400
  code-display:
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
    fontSize: "16px"
    fontWeight: 700
    letterSpacing: "0.08em"
  code-display-lg:
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
    fontSize: "28px"
    fontWeight: 700
    letterSpacing: "0.16em"
  code-inline:
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
    fontSize: "11px"
    fontWeight: 400
  code-id:
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
    fontSize: "11px"
    fontWeight: 500
rounded:
  xs: "4px"
  sm: "6px"
  md: "8px"
  pill: "999px"
spacing:
  xs: "4px"
  sm: "6px"
  md: "8px"
  lg: "10px"
  xl: "12px"
  xxl: "16px"
components:
  button-primary:
    backgroundColor: "{colors.signal-blue}"
    textColor: "{colors.on-signal}"
    typography: "{typography.body}"
    rounded: "{rounded.sm}"
    padding: "7px 10px"
  button-secondary:
    backgroundColor: "{colors.quiet-fill}"
    textColor: "{colors.quiet-ink}"
    typography: "{typography.body}"
    rounded: "{rounded.sm}"
    padding: "7px 10px"
  button-danger:
    backgroundColor: "transparent"
    textColor: "{colors.redline}"
    typography: "{typography.body}"
    rounded: "{rounded.sm}"
    padding: "7px 10px"
  icon-button:
    backgroundColor: "{colors.quiet-fill}"
    textColor: "{colors.quiet-ink}"
    rounded: "{rounded.sm}"
    size: "28px"
  card:
    backgroundColor: "{colors.panel-white}"
    rounded: "{rounded.md}"
    padding: "12px"
  job-row:
    backgroundColor: "{colors.inset-wash}"
    textColor: "{colors.ink-readout}"
    typography: "{typography.body}"
    rounded: "{rounded.sm}"
    padding: "8px"
  chip:
    backgroundColor: "{colors.chip-fill}"
    textColor: "{colors.chip-ink}"
    typography: "{typography.label}"
    rounded: "{rounded.pill}"
    padding: "4px 8px"
  input:
    backgroundColor: "{colors.inset-wash}"
    textColor: "{colors.ink-readout}"
    typography: "{typography.body-lg}"
    rounded: "{rounded.sm}"
    padding: "7px 8px"
  code-display:
    backgroundColor: "{colors.inset-wash}"
    textColor: "{colors.ink-readout}"
    typography: "{typography.code-display}"
    rounded: "{rounded.sm}"
    padding: "8px 10px"
---

# Design System: Lecture Transcripts

## Overview

**Creative North Star: "The Flight Recorder"**

The extension chrome is the instrument panel of a capture pipeline that must never lose a recording. Every surface reports state the way a cockpit readout does: counts, connection status, queue depth, last outcome. Nothing on screen exists to please; everything exists to tell the operator what the pipeline has done and what it still owes. The result is utilitarian calm — dense, quiet, and impossible to misread in the two seconds a student spends checking whether the lecture actually uploaded.

This is a GitHub-adjacent operational aesthetic: system typography, hairline-bordered panels, one action blue, and status colors that mean exactly what they mean in a runbook. The popup is a fixed 390px canvas stacked with full-width sections; the options page widens to a calm 760px column without changing its vocabulary. Both ship as paired light/dark token sets so the panel reads correctly at 2am the night before an exam.

**Key Characteristics:**
- Flat instrument panel: hairline borders and tonal steps only — never shadows
- Dense system-typography scale (11–18px) tuned for a fixed 390px popup
- One action accent (Signal Blue), reserved for primary actions, links, and focus rings
- Status as color-coded readouts — amber, green, red — always paired with plain words
- Every surface color ships as a light/dark pair selected by `prefers-color-scheme`

## Colors

A cool, low-chroma operational palette with exactly one accent; the values share lineage with GitHub Primer, which keeps the uploader's GitHub-facing work feeling of one piece.

### Primary
- **Signal Blue** (#1f6feb, dark #2270e6): The only action accent. Primary buttons, the GitHub authorization link, focus rings, and native-control accent color. Never a chrome fill.
- **On-Signal** (#ffffff): Text and icons on Signal Blue fills.

### Neutral
- **Cool Chassis** (#f5f7fa, dark #12161d): The page/popup background — the chassis everything is mounted on.
- **Ink Readout** (#172033, dark #e6eaf2): Primary text. Headings, job titles, field values.
- **Slate Mute** (#687286, dark #9aa7bb): Secondary text — subtitles, metadata, field labels, footer versions.
- **Panel White** (#ffffff, dark #1a2029): Card and settings-section surfaces sitting on the chassis.
- **Hairline** (#dfe4ec, dark #2a3340): 1px borders on cards, fields, and the error list. The system's only structural line.
- **Chip Fill** (#e6eaf0, dark #232b36) / **Chip Ink** (#4f5d72, dark #b6c2d4): Status pills, counts, term chips, inline code backgrounds.
- **Quiet Fill** (#e9edf3, dark #242c37) / **Quiet Ink** (#26344c, dark #d5dce8): Secondary and icon buttons — available but not shouting.
- **Inset Wash** (#f7f9fc, dark #1f2731): Recessed readouts — job rows, form fields, code chips. One step deeper than the card it sits in.

### Status
- **Caution Amber** (#6b4b00, dark #e3b341): Attention notices — pending handoffs, expiring authorization, warnings.
- **Verified Green** (#186b31, dark #3fb950): Success notices only; the color of a durably accepted capture. Always paired with weight 600.
- **Redline** (#c2261f, dark #ff7b72) / **Redline Wash** (rgba(194,38,31,0.08), dark rgba(248,81,73,0.15)): Errors, invalid fields, destructive controls, and their tinted backgrounds.

### Named Rules
**The One Accent Rule.** Signal Blue is reserved for primary actions, links, and focus rings. Its rarity is what makes the next action findable; it is never chrome, never decoration, never a status.

**The Scheme-Pair Rule.** Every surface color ships as a light/dark pair selected by `prefers-color-scheme`. Never build a screen from the light values alone — the 2am case is a first-class scene.

**The Words-First Rule.** Status colors only ever accompany plain words. Green means nothing without "uploaded"; red means nothing without the failure it reports.

## Typography

**Display Font:** none — this system has no display voice.
**Body Font:** -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif
**Label/Mono Font:** ui-monospace, SFMono-Regular, Menlo, Consolas, monospace

**Character:** System-font utilitarianism. No webfonts, no personality beyond legibility at small sizes in a small panel; the mono stack appears only where text is machine evidence.

### Hierarchy
- **Headline** (700, 18px / 1.25): Popup page title. The options page steps up to 22px for its single h1.
- **Title** (700, 14px / 1.3): Section headings inside cards ("Pending handoffs", "Upload queue"). Options page uses 15px for the same role.
- **Body** (400, 12px): Default copy — notices, job rows, buttons, header summary. The popup's working size.
- **Body Large** (400, 13px): Options-page form copy and input values; the slightly larger reading size of the long-lived page.
- **Label** (600, 11px): Pills and counts. Tabular, compact, scannable. Course legends sit one step up at 12px/600 in Slate Mute.
- **Meta** (400, 11px): Job metadata, footer versions — the quietest legible tier.
- **Code Display** (700, 16px, letter-spacing 0.08em, mono): Device-flow authorization codes in the popup, shown large and tracked so they can be read off one screen and typed into another.
- **Code Display Large** (700, 28px, letter-spacing 0.16em, mono): The authorization page's full-width code chip — bigger than any headline in the system, because it must be transcribed by hand.
- **Code Inline** (400, 11px, mono): Inline code in options copy (`YYYY-fall`, `012`) on Chip Fill at 4px radius.
- **Code ID** (500, 11px, mono): Extension ID chips — Slate Mute on Inset Wash, truncated with ellipsis past 150px.

### Named Rules
**The Mono-Is-Evidence Rule.** Monospace marks machine facts — authorization codes, IDs, raw labels. Prose is never mono, and mono is never used for emphasis.

## Layout

A fixed instrument popup and one wide settings column; there is no responsive breakpoint behavior to design, because the surfaces are fixed by the platform. The popup body is exactly 390px wide with a 440px minimum height and 16px panel padding (the width is scoped to the `.popup-shell` body class so the shared sheet cannot cap the other pages); sections stack full-width. The options page is a fluid-width document with a 760px centered column (padding 24px 20px 40px); the auth page is a 460px centered column whose code chip goes full-width at the large code size, with its copy button stacked full-width beneath.

The spacing rhythm runs 4 / 6 / 8 / 10 / 12 / 14 / 16px, with 20 / 24 / 28px reserved for page-level breathing room on the options page. 8px is the default gap between controls; cards stack at 12px margins; inner card padding is 12px (settings sections 14px). Form fields use a reflowing grid (`auto-fit, minmax(200px, 1fr)`) that collapses to one column on narrow documents — the only adaptive layout in the system.

## Elevation & Depth

This system is flat by doctrine. There is not a single `box-shadow` in the codebase, and depth is conveyed entirely by 1px Hairline borders and a three-step tonal ladder: Cool Chassis (page) → Panel White (card) → Inset Wash (recessed rows, fields, code chips). A recessed element is always one tonal step *deeper* than its container, never raised above it.

### Named Rules
**The Flat-by-Structure Rule.** Surfaces are separated by borders and tonal steps. No box-shadows, glows, gradients, or backdrop filters — a shadow in this system is a bug, not a style.

## Shapes

Gently rounded, engineered, unfussy. Default controls sit at 6px radius (buttons, icon buttons, inputs, selects, job rows, code chips); containers open to 8px (cards, settings sections, error lists); tiny inline code tightens to 4px; pills, counts, and term chips go fully round (999px). Borders are 1px solid Hairline wherever a boundary is needed. Buttons are borderless except the danger variant, which draws a 1px Redline outline over a transparent fill. No decorative geometry, no clipping, no illustration.

## Components

Compact utilitarian controls: precise hit targets, immediate state changes, factual feedback.

### Buttons
- **Shape:** Gently rounded (6px radius); borderless except danger.
- **Primary:** Signal Blue fill, On-Signal text, 12px type, padding 7px 10px.
- **Hover / Focus:** No transition — states snap. Fill buttons have no hover change; icon buttons brighten their Quiet Fill (`filter: brightness(0.95)`), and the danger variant tints Redline Wash. Focus-visible draws a 2px Signal Blue outline at 1px offset on every control (2px offset on the settings toggles).
- **Secondary:** Quiet Fill / Quiet Ink for available-but-not-primary actions (Refresh, Reset, Reload).
- **Danger:** Transparent fill, 1px Redline border, Redline text; hover fills with Redline Wash. Used for remove/discard and validation failures.
- **Disabled:** 55% opacity, `not-allowed` cursor. Same shape, no other change.
- **Icon Button:** 28×28px square at 6px radius, Quiet Fill, 16px inline SVG icon in Quiet Ink (e.g. the settings gear in the popup header).

### Chips
- **Style:** Fully round (999px), Chip Fill on Chip Ink. Status pills and counts run 11px label type at 4px 8px padding; term chips run 12px at 3px 6px 3px 10px to seat their inline remove control.
- **State:** Removable term chips (options page) carry a remove glyph that turns Redline on hover.

### Cards / Containers
- **Corner Style:** 8px radius.
- **Background:** Panel White on the Cool Chassis.
- **Shadow Strategy:** None — see Elevation & Depth. Separation is the 1px Hairline border plus the tonal step.
- **Border:** 1px solid Hairline.
- **Internal Padding:** 12px (popup cards); settings sections 14px. Section headings inside cards get 0 top margin.

### Inputs / Fields
- **Style:** Inset Wash fill, 1px Hairline border, 6px radius, padding 7px 8px, 13px text in Ink Readout. Labels sit above at 12px Slate Mute in a 4px-gap grid.
- **Focus:** 2px Signal Blue outline at 1px offset. Toggles are native checkboxes with `accent-color` set to Signal Blue.
- **Error:** `aria-invalid` switches the border and outline to Redline; the error list below tints its background with Redline Wash inside a 1px Redline border (8px radius).

### Navigation
- **Style:** No nav hierarchy. A header row (title, muted status line, icon button, status pill) and a footer row (version line, truncated extension ID with copy action), both flex rows at 10px gap. Hidden sections use a hard `display: none` — state is content, not chrome.

### Status Notices (signature)
One-line outcome readouts, 12px text, minimum 16px tall so the layout never jumps: `notice-info` in Slate Mute, `notice-warn` in Caution Amber, `notice-success` in Verified Green at weight 600, `notice-error` in Redline. The unclassed resting tone of a `.notice` is Slate Mute (the info level); amber, green, and red only appear with their explicit modifier class. They live in `aria-live` regions — the color reports the outcome, the words state it. Device-flow countdowns are the exception: the per-second tick is `aria-live="off"` and only the three milestones (ready, under a minute, expired) go to a visually-hidden polite announcer.

### Job Rows (signature)
Recessed Inset Wash rows (6px radius, 8px padding) in a tight grid list: the lecture title at 12px/600 on the left — the capture's topic ("Intro, Smith") or the resolved identity ("Lecture 6") when the Leccap title was still a lag form, never the `lectureKey` — a compact status chip on the right (11px/600, fully round, tinted wash background in the semantic color), metadata at 11px Slate Mute below (human date, target path, an 8-character hash), and smaller nested action buttons (11px, padding 5px 8px). Each row is one capture in the pipeline — the visual atom of the flight recorder. Status chips pair the semantic color with the status words so the queue is scannable at a glance. A chip never shrinks its title into a vertical column: the title keeps at least 12ch and a chip that cannot share the line wraps beneath it.

### Code Chips (signature)
Mono readouts on Inset Wash at 6px radius: device-flow codes at 16px/700 with 0.08em tracking in the popup (larger than any headline there — they must be transcribed), escalating to 28px/700 with 0.16em tracking, full-width and centered, on the authorization page. Extension IDs run 11px/500 in Slate Mute, truncated with ellipsis past 150px.

## Do's and Don'ts

### Do:
- **Do** separate surfaces with 1px Hairline borders and the Cool Chassis → Panel White → Inset Wash tonal ladder.
- **Do** ship every color as a light/dark pair and let `prefers-color-scheme` pick; test both schemes.
- **Do** reserve Signal Blue for primary actions, links, and focus rings — and give every interactive control that 2px focus outline (1px offset; 2px on the settings toggles).
- **Do** use Verified Green, Caution Amber, and Redline only to report outcome state, always next to plain words.
- **Do** hold the type scale: 11px labels/meta, 12px popup body, 13px options form copy, 14px (15px options) section titles, 18px (22px options) page titles — and let only the mono code readouts break it (16px popup, 28px authorization page).
- **Do** render machine facts — codes, IDs, raw labels — in the mono stack.

### Don't:
- **Don't** introduce box-shadows, glows, gradients, or backdrop filters; depth is borders and tonal steps only.
- **Don't** use monospace for prose, or Signal Blue as decorative chrome.
- **Don't** add webfonts or decorative type; the system stack is the identity.
- **Don't** add motion for its own sake — state changes are instant today; introduce transitions only with a stated reason.
- **Don't** override native form affordances beyond `accent-color` and focus outlines.
- **Don't** let status colors appear without the words they color.
