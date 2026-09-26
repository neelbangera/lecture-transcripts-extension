/**
 * Design-rule regression tests.
 *
 * These lock the critique's accessibility fixes: WCAG AA text contrast on
 * every token pairing, a visible focus ring in both schemes, the auth page's
 * documented 460px column, and device-flow countdowns kept out of live
 * regions. They read the CSS/HTML artifacts directly so a token change cannot
 * silently reintroduce the defect.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

import {
  EXPIRY_MILESTONE_ANNOUNCEMENTS,
  displayTitleFor,
  expiryMilestone,
  formatCapturedAt,
  formatLectureDate,
  identityFromSummary,
} from "../extension/src/status";

const popupCss = readFileSync(
  fileURLToPath(new URL("../extension/popup.css", import.meta.url)),
  "utf8",
);
const popupHtml = readFileSync(
  fileURLToPath(new URL("../extension/popup.html", import.meta.url)),
  "utf8",
);
const authHtml = readFileSync(
  fileURLToPath(new URL("../extension/auth.html", import.meta.url)),
  "utf8",
);

function relativeLuminance(hex: string): number {
  let normalized = hex.replace("#", "").toLowerCase();
  if (normalized.length === 3) {
    normalized = normalized
      .split("")
      .map((character) => character + character)
      .join("");
  }
  if (!/^[0-9a-f]{6}$/.test(normalized)) {
    throw new Error(`not a six-digit hex color: ${hex}`);
  }
  const channels = [0, 2, 4]
    .map((index) => parseInt(normalized.slice(index, index + 2), 16) / 255)
    .map((value) =>
      value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4,
    );
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
}

function contrastRatio(foreground: string, background: string): number {
  const values = [relativeLuminance(foreground), relativeLuminance(background)].sort(
    (a, b) => b - a,
  );
  return (values[0] + 0.05) / (values[1] + 0.05);
}

/** Extract the `--token: #hex` pairs declared inside one CSS block. */
function tokensIn(block: string): Record<string, string> {
  const tokens: Record<string, string> = {};
  for (const match of block.matchAll(/--([a-z-]+):\s*(#[0-9a-fA-F]{3,8})\s*;/g)) {
    tokens[match[1]] = match[2];
  }
  return tokens;
}

/** Extract the `--token: rgba(r, g, b, a)` washes declared inside one block. */
function washesIn(block: string): Record<string, [number, number, number, number]> {
  const washes: Record<string, [number, number, number, number]> = {};
  for (const match of block.matchAll(
    /--([a-z-]+):\s*rgba\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*,\s*([0-9.]+)\s*\)\s*;/g,
  )) {
    washes[match[1]] = [
      Number(match[2]),
      Number(match[3]),
      Number(match[4]),
      Number(match[5]),
    ];
  }
  return washes;
}

function hexToRgb(hex: string): [number, number, number] {
  let normalized = hex.replace("#", "").toLowerCase();
  if (normalized.length === 3) {
    normalized = normalized
      .split("")
      .map((character) => character + character)
      .join("");
  }
  return [
    parseInt(normalized.slice(0, 2), 16),
    parseInt(normalized.slice(2, 4), 16),
    parseInt(normalized.slice(4, 6), 16),
  ];
}

function rgbToHex([r, g, b]: [number, number, number]): string {
  return `#${[r, g, b]
    .map((value) => Math.round(value).toString(16).padStart(2, "0"))
    .join("")}`;
}

/** Composite a translucent wash over an opaque backdrop into one hex. */
function compositeWash(
  wash: [number, number, number, number],
  backdropHex: string,
): string {
  const [wr, wg, wb, alpha] = wash;
  const [br, bg, bb] = hexToRgb(backdropHex);
  return rgbToHex([
    wr * alpha + br * (1 - alpha),
    wg * alpha + bg * (1 - alpha),
    wb * alpha + bb * (1 - alpha),
  ]);
}

function schemeTokens(): {
  light: Record<string, string>;
  dark: Record<string, string>;
  lightWashes: Record<string, [number, number, number, number]>;
  darkWashes: Record<string, [number, number, number, number]>;
} {
  const rootMatch = popupCss.match(/:root\s*\{([\s\S]*?)\n\}/);
  const darkMatch = popupCss.match(
    /@media \(prefers-color-scheme: dark\)\s*\{\s*:root\s*\{([\s\S]*?)\n\s*\}/,
  );
  if (!rootMatch || !darkMatch) throw new Error("popup.css token blocks not found");
  return {
    light: tokensIn(rootMatch[1]),
    dark: tokensIn(darkMatch[1]),
    lightWashes: washesIn(rootMatch[1]),
    darkWashes: washesIn(darkMatch[1]),
  };
}

describe("WCAG contrast on design tokens", () => {
  const { light, dark, lightWashes, darkWashes } = schemeTokens();

  it("parses a full light and dark token set", () => {
    expect(Object.keys(light).length).toBeGreaterThan(10);
    expect(Object.keys(dark).length).toBeGreaterThan(10);
  });

  for (const [name, tokens] of [
    ["light", light],
    ["dark", dark],
  ] as const) {
    it(`${name} scheme text pairings meet AA (4.5:1)`, () => {
      const pairs: Array<[string, string, string]> = [
        ["primary buttons", tokens["primary-fg"], tokens["primary-bg"]],
        ["body text", tokens["fg"], tokens["bg"]],
        ["secondary text", tokens["muted"], tokens["bg"]],
        ["secondary buttons", tokens["secondary-fg"], tokens["secondary-bg"]],
        ["pill labels", tokens["pill-fg"], tokens["pill-bg"]],
      ];
      for (const [label, foreground, background] of pairs) {
        expect(foreground, `missing token for ${label}`).toBeTruthy();
        expect(background, `missing token for ${label}`).toBeTruthy();
        expect(
          contrastRatio(foreground, background),
          `${name} ${label} (${foreground} on ${background})`,
        ).toBeGreaterThanOrEqual(4.5);
      }
    });

    it(`${name} scheme focus ring is visible (3:1)`, () => {
      const ring = tokens["primary-bg"];
      for (const [label, surface] of [
        ["page background", tokens["bg"]],
        ["card surface", tokens["card-bg"]],
      ] as const) {
        expect(
          contrastRatio(ring, surface),
          `${name} focus ring on ${label}`,
        ).toBeGreaterThanOrEqual(3);
      }
    });

    it(`${name} scheme status chips meet AA over their wash`, () => {
      const washes = name === "light" ? lightWashes : darkWashes;
      const jobBg = tokens["job-bg"];
      // Chips composite their semantic wash over the job row background, so
      // the ink has to clear AA against the blended color, not the raw wash.
      const chips: Array<[string, string, string]> = [
        ["success chip", tokens["success"], "success-bg"],
        ["warn chip", tokens["notice"], "warn-bg"],
        ["error chip", tokens["danger"], "danger-bg"],
      ];
      for (const [label, ink, washToken] of chips) {
        const wash = washes[washToken];
        expect(wash, `${name} missing ${washToken}`).toBeTruthy();
        const surface = compositeWash(wash, jobBg);
        expect(
          contrastRatio(ink, surface),
          `${name} ${label} (${ink} on ${surface})`,
        ).toBeGreaterThanOrEqual(4.5);
      }
      // The neutral chip is ink on an opaque fill.
      expect(
        contrastRatio(tokens["pill-fg"], tokens["pill-bg"]),
        `${name} neutral chip`,
      ).toBeGreaterThanOrEqual(4.5);
    });

    it(`${name} scheme notice inks meet AA on their surfaces`, () => {
      for (const [label, ink] of [
        ["success notice", tokens["success"]],
        ["warn notice", tokens["notice"]],
        ["error notice", tokens["danger"]],
      ] as const) {
        expect(
          contrastRatio(ink, tokens["card-bg"]),
          `${name} ${label} on card`,
        ).toBeGreaterThanOrEqual(4.5);
      }
    });
  }

  it("keeps the dark primary distinct from the light one", () => {
    // The light and dark fills are a paired set; collapsing them to one value
    // is how the dark-mode AA failure was introduced in the first place.
    expect(light["primary-bg"]).not.toBe(dark["primary-bg"]);
  });
});

describe("popup and auth page structure", () => {
  it("scopes the fixed 390px canvas to the popup shell only", () => {
    expect(popupCss).toMatch(/\.popup-shell\s*\{[^}]*width:\s*390px/);
    expect(popupHtml).toContain('<body class="popup-shell">');
    // The auth page must not inherit the popup width; its 460px column wins.
    expect(authHtml).not.toContain("popup-shell");
    expect(authHtml).toMatch(/\.auth-page\s*\{[^}]*max-width:\s*460px/);
  });

  it("keeps the per-second countdown out of live regions", () => {
    // The ticking nodes carry aria-live="off"; milestones go to a separate
    // visually-hidden polite announcer.
    expect(popupHtml).toMatch(
      /id="authorization-expiry"[^>]*aria-live="off"/,
    );
    expect(authHtml).toMatch(/id="auth-expiry"[^>]*aria-live="off"/);
    expect(popupHtml).toMatch(
      /id="authorization-announce"[^>]*aria-live="polite"/,
    );
    expect(authHtml).toMatch(/id="auth-announce"[^>]*aria-live="polite"/);
    expect(popupCss).toContain(".visually-hidden");
  });
});

describe("expiry milestone announcements", () => {
  it("classifies ready, under-a-minute, and expired", () => {
    const now = Date.parse("2026-09-23T12:00:00Z");
    expect(expiryMilestone(null, now)).toBe("ready");
    expect(expiryMilestone("not a date", now)).toBe("ready");
    expect(expiryMilestone("2026-09-23T12:05:00Z", now)).toBe("ready");
    expect(expiryMilestone("2026-09-23T12:00:30Z", now)).toBe("under-a-minute");
    expect(expiryMilestone("2026-09-23T12:00:00Z", now)).toBe("expired");
    expect(expiryMilestone("2026-09-23T11:59:00Z", now)).toBe("expired");
  });

  it("has one announcement per milestone", () => {
    expect(Object.keys(EXPIRY_MILESTONE_ANNOUNCEMENTS).sort()).toEqual([
      "expired",
      "ready",
      "under-a-minute",
    ]);
    for (const text of Object.values(EXPIRY_MILESTONE_ANNOUNCEMENTS)) {
      expect(text.length).toBeGreaterThan(0);
    }
  });

  it("renders overflow capture times as compact UTC, never a raw ISO stamp", () => {
    expect(formatCapturedAt("2026-09-21T18:02:00Z")).toBe("2026-09-21 18:02");
    // A malformed stamp stays visible instead of being hidden by a guess.
    expect(formatCapturedAt("not-a-stamp")).toBe("not-a-stamp");
  });
});

describe("student-facing row naming", () => {
  it("shows the lecture topic when the capture has one", () => {
    expect(displayTitleFor("lecture", 3, "Intro, Smith")).toBe("Intro, Smith");
    expect(displayTitleFor("discussion", 2, "Smith")).toBe("Smith");
  });

  it("synthesizes the resolved identity when the title was a lag form", () => {
    expect(displayTitleFor("lecture", 6, null)).toBe("Lecture 6");
    expect(displayTitleFor("discussion", 2, "")).toBe("Discussion 2");
    expect(displayTitleFor("lecture", 6, "   ")).toBe("Lecture 6");
  });

  it("formats lecture dates for a student rather than as ISO", () => {
    expect(formatLectureDate("2026-09-08")).toBe("Sep 8, 2026");
    expect(formatLectureDate("2026-01-31")).toBe("Jan 31, 2026");
    expect(formatLectureDate(null)).toBe("");
    expect(formatLectureDate("not-a-date")).toBe("not-a-date");
  });

  it("reads the kind and number back out of a queue row", () => {
    const base = {
      jobId: 1,
      contentHash: "a".repeat(64),
      status: "queued",
      attemptCount: 0,
      nextAttemptAt: null,
      updatedAt: null,
      lastErrorCategory: null,
      lastErrorHttpStatus: null,
      remoteContentHash: null,
      remoteFileKind: null,
      lectureDate: "2026-09-08",
      displayTitle: "Intro, Smith",
    } as const;
    expect(
      identityFromSummary({
        ...base,
        lectureKey: "eecs484/2026-fall/003",
        targetPath: "eecs484/003.md",
      }),
    ).toEqual({ kind: "lecture", lectureNumber: 3 });
    expect(
      identityFromSummary({
        ...base,
        lectureKey: "eecs484/2026-fall/002",
        targetPath: "eecs484/discussions/002.md",
      }),
    ).toEqual({ kind: "discussion", lectureNumber: 2 });
  });

  it("never prints a lectureKey as the row headline", () => {
    // The topic wins; the synthesized fallback names the identity in words.
    for (const headline of [
      displayTitleFor("lecture", 3, "Intro, Smith"),
      displayTitleFor("lecture", 3, null),
    ]) {
      expect(headline).not.toMatch(/[a-z]\w*\/\d{4}-/);
    }
  });
});
