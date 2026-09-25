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
  expiryMilestone,
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

function schemeTokens(): { light: Record<string, string>; dark: Record<string, string> } {
  const rootMatch = popupCss.match(/:root\s*\{([\s\S]*?)\n\}/);
  const darkMatch = popupCss.match(
    /@media \(prefers-color-scheme: dark\)\s*\{\s*:root\s*\{([\s\S]*?)\n\s*\}/,
  );
  if (!rootMatch || !darkMatch) throw new Error("popup.css token blocks not found");
  return { light: tokensIn(rootMatch[1]), dark: tokensIn(darkMatch[1]) };
}

describe("WCAG contrast on design tokens", () => {
  const { light, dark } = schemeTokens();

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
});
