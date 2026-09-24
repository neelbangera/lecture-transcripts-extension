/**
 * Auth helper page tests.
 *
 * `auth.ts` self-installs on import unless `__AUTH_PAGE_TEST__` is set, so
 * tests mount the real `auth.html`, set that flag, and call `installAuthPage`
 * with an explicit query string.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { JSDOM } from "jsdom";
import { afterEach, describe, expect, it, vi } from "vitest";

const authHtml = readFileSync(
  fileURLToPath(new URL("../extension/auth.html", import.meta.url)),
  "utf8",
);

type AuthModule = typeof import("../extension/src/auth");

let dom: JSDOM;
let openedTabs: string[] = [];
let copiedTexts: string[] = [];

function element<T extends HTMLElement>(id: string): T {
  const found = dom.window.document.getElementById(id);
  if (!found) throw new Error(`missing auth element: ${id}`);
  return found as unknown as T;
}

function query(search: string): string {
  return search.startsWith("?") ? search : `?${search}`;
}

async function mountAuth(search: string): Promise<AuthModule> {
  dom = new JSDOM(authHtml, {
    url: `chrome-extension://abcdefghijklmnopqrstuvwxyzabcdef/auth.html${query(search)}`,
  });
  openedTabs = [];
  copiedTexts = [];
  const globals = globalThis as Record<string, unknown>;
  globals.window = dom.window;
  globals.document = dom.window.document;
  globals.__AUTH_PAGE_TEST__ = true;
  vi.stubGlobal("navigator", {
    clipboard: {
      writeText: async (value: string) => {
        copiedTexts.push(value);
      },
    },
  });
  globals.chrome = {
    tabs: {
      create: ({ url }: { url: string }) => {
        openedTabs.push(url);
      },
    },
  };
  vi.resetModules();
  const mod = await import("../extension/src/auth");
  return mod;
}

afterEach(() => {
  const globals = globalThis as Record<string, unknown>;
  delete globals.window;
  delete globals.document;
  delete globals.chrome;
  delete globals.__AUTH_PAGE_TEST__;
  vi.unstubAllGlobals();
  vi.resetModules();
});

describe("auth page rendering", () => {
  const CODE = "ABCD-1234";
  const SAFE_URL = "https://github.com/login/device?user_code=ABCD-1234";
  const EXPIRES = "2100-01-01T00:00:00Z";

  function searchFor(overrides: Partial<{ code: string; url: string; expires: string }> = {}): string {
    const params = new URLSearchParams();
    params.set("code", overrides.code ?? CODE);
    params.set("url", overrides.url ?? SAFE_URL);
    params.set("expires", overrides.expires ?? EXPIRES);
    return params.toString();
  }

  it("shows the code card, expiry countdown, keychain note, and fallback copy", async () => {
    const mod = await mountAuth(searchFor());
    mod.installAuthPage(query(searchFor()));
    expect(element("auth-code").textContent).toBe(CODE);
    expect(element("auth-code-row").classList.contains("hidden")).toBe(false);
    expect(element("auth-expiry").textContent).toContain("expires in");
    expect(element("auth-keychain").textContent).toContain("Always Allow");
    expect(element("auth-fallback").textContent).toContain("github.com/login/device");
  });

  it("auto-opens the safe GitHub approval URL once", async () => {
    const mod = await mountAuth(searchFor());
    mod.installAuthPage(query(searchFor()));
    expect(openedTabs).toEqual([SAFE_URL]);
    expect(element<HTMLAnchorElement>("auth-github-link").href).toContain(
      "github.com/login/device",
    );
  });

  it("never opens a non-GitHub verification URL and keeps the manual fallback", async () => {
    const mod = await mountAuth(
      searchFor({ url: "https://example.com/login/device?user_code=ABCD-1234" }),
    );
    mod.installAuthPage(
      query(searchFor({ url: "https://example.com/login/device?user_code=ABCD-1234" })),
    );
    expect(openedTabs).toEqual([]);
    expect(element("auth-github-link").classList.contains("hidden")).toBe(true);
    expect(element("auth-fallback").classList.contains("hidden")).toBe(false);
    expect(element("auth-code").textContent).toBe(CODE);
  });

  it("copies the code on demand", async () => {
    const mod = await mountAuth(searchFor());
    mod.installAuthPage(query(searchFor()));
    element<HTMLButtonElement>("auth-copy-code").click();
    await Promise.resolve();
    await Promise.resolve();
    expect(copiedTexts).toEqual([CODE]);
  });

  it("reports an expired code instead of a countdown", async () => {
    const mod = await mountAuth(searchFor({ expires: "2000-01-01T00:00:00Z" }));
    mod.installAuthPage(query(searchFor({ expires: "2000-01-01T00:00:00Z" })), {
      now: () => Date.parse("2026-09-23T12:00:00Z"),
    });
    expect(element("auth-expiry").textContent).toContain("This code expired");
  });

  it("parses params and formats expiry as pure helpers", async () => {
    const mod = await mountAuth("code=X&url=https%3A%2F%2Fgithub.com%2Flogin%2Fdevice&expires=2100-01-01T00:00:00Z");
    expect(mod.authPageParams("?code=%20ABCD-1234%20&url=&expires=")).toEqual({
      code: "ABCD-1234",
      url: null,
      expires: null,
    });
    expect(mod.formatExpiry(null, Date.now())).toBe("Waiting for approval on GitHub…");
    expect(
      mod.formatExpiry("2000-01-01T00:00:00Z", Date.parse("2026-09-23T12:00:00Z")),
    ).toContain("This code expired");
    expect(
      mod.formatExpiry("2100-01-01T00:01:30Z", Date.parse("2100-01-01T00:00:00Z")),
    ).toBe("Waiting for approval on GitHub… expires in 1:30");
    expect(mod.isSafeGitHubUrl("https://github.com/login/device")).toBe(true);
    expect(mod.isSafeGitHubUrl("https://example.com/")).toBe(false);
    expect(mod.isSafeGitHubUrl("not a url")).toBe(false);
  });
});
