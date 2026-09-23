import { describe, expect, it, vi } from "vitest";

import {
  canOpenOptionsPage,
  openOptionsPage,
} from "../extension/src/settings-actions";

describe("settings entry point helpers", () => {
  it("reports availability only when openOptionsPage is a function", () => {
    expect(canOpenOptionsPage(undefined)).toBe(false);
    expect(canOpenOptionsPage(null)).toBe(false);
    expect(canOpenOptionsPage({})).toBe(false);
    expect(canOpenOptionsPage({ openOptionsPage: () => undefined })).toBe(true);
  });

  it("opens the options page and reports success", () => {
    const open = vi.fn();
    expect(openOptionsPage({ openOptionsPage: open })).toBe(true);
    expect(open).toHaveBeenCalledTimes(1);
  });

  it("reports failure without throwing when the API is missing or throws", () => {
    expect(openOptionsPage(undefined)).toBe(false);
    expect(openOptionsPage({})).toBe(false);
    expect(
      openOptionsPage({
        openOptionsPage: () => {
          throw new Error("no options page");
        },
      }),
    ).toBe(false);
  });
});
