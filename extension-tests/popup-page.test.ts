/**
 * Popup rendering tests.
 *
 * The popup module self-initializes on import, so each test mounts the real
 * `popup.html` in JSDOM, installs a stubbed `chrome.runtime.sendMessage`, and
 * imports the module with a fresh registry.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { JSDOM } from "jsdom";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  EMPTY_COUNTS,
  type CountMap,
  type ExtensionSnapshot,
  type JobSummary,
  type LastOutcome,
  type UploaderStatus,
} from "../extension/src/status";

const popupHtml = readFileSync(
  fileURLToPath(new URL("../extension/popup.html", import.meta.url)),
  "utf8",
);

const JOB: JobSummary = {
  jobId: 1,
  lectureKey: "EECS 484/2026-fall/lecture-01",
  contentHash: "abc",
  status: "uploaded",
  attemptCount: 1,
  nextAttemptAt: null,
  updatedAt: null,
  targetPath: "eecs484/2026-fall/lecture-01.txt",
  lastErrorCategory: null,
  lastErrorHttpStatus: null,
  remoteContentHash: null,
  remoteFileKind: null,
  lectureDate: "2026-09-08",
  displayTitle: "Intro, Smith",
};

function makeSnapshot(
  options: {
    jobs?: JobSummary[];
    lastOutcome?: LastOutcome | null;
    authState?: UploaderStatus["authState"];
    authorization?: UploaderStatus["authorization"];
  } = {},
): ExtensionSnapshot {
  const jobs = options.jobs ?? [];
  const counts: CountMap = { ...EMPTY_COUNTS };
  for (const job of jobs) counts[job.status] += 1;
  const uploader: UploaderStatus = {
    type: "status",
    protocolVersion: 1,
    requestId: null,
    extensionVersion: "0.1.0",
    uploaderVersion: "0.4.2",
    authState: options.authState ?? "connected",
    authorization: options.authorization ?? {
      userCode: null,
      verificationUri: null,
      verificationUriComplete: null,
      expiresAt: null,
    },
    drainState: "idle",
    counts,
    jobs,
    nextBeforeJobId: null,
  };
  return {
    uploader,
    pendingHandoffs: 0,
    overflowNotices: [],
    lastOutcome: options.lastOutcome ?? null,
    updatedAt: "2026-09-23T12:00:00Z",
  };
}

const OUTCOME: LastOutcome = {
  status: "uploaded",
  lectureKey: "EECS 484/2026-fall/lecture-01",
  contentHash: "abc",
  message: "",
  action: "none",
  at: "2026-09-23T12:00:00Z",
};

type Responder = (message: { type: string }) => unknown;

let dom: JSDOM;
let openedTabs: string[] = [];
let copiedTexts: string[] = [];

function element<T extends HTMLElement>(id: string): T {
  const found = dom.window.document.getElementById(id);
  if (!found) throw new Error(`missing popup element: ${id}`);
  return found as unknown as T;
}

async function settle(rounds = 8): Promise<void> {
  for (let round = 0; round < rounds; round += 1) {
    await new Promise<void>((resolve) => setTimeout(resolve, 0));
  }
}

async function mountPopup(respond: Responder): Promise<void> {
  dom = new JSDOM(popupHtml);
  openedTabs = [];
  copiedTexts = [];
  const globals = globalThis as Record<string, unknown>;
  globals.window = dom.window;
  globals.document = dom.window.document;
  vi.stubGlobal("navigator", {
    clipboard: {
      writeText: async (value: string) => {
        copiedTexts.push(value);
      },
    },
  });
  globals.chrome = {
    runtime: {
      id: "abcdefghijklmnopqrstuvwxyzabcdef",
      getURL: (path: string) =>
        `chrome-extension://abcdefghijklmnopqrstuvwxyzabcdef/${path}`,
      sendMessage: (message: { type: string }, callback: (response: unknown) => void) => {
        callback(respond(message));
      },
      openOptionsPage: () => undefined,
    },
    tabs: {
      create: ({ url }: { url: string }) => {
        openedTabs.push(url);
      },
    },
  };
  vi.resetModules();
  await import("../extension/src/popup");
  await settle();
}

afterEach(() => {
  const globals = globalThis as Record<string, unknown>;
  delete globals.window;
  delete globals.document;
  delete globals.chrome;
  vi.unstubAllGlobals();
  vi.resetModules();
});

describe("popup queue and outcome states", () => {
  it("shows the empty queue copy when no jobs are loaded", async () => {
    await mountPopup(() => ({ ok: true, snapshot: makeSnapshot() }));
    expect(element("queue-copy").classList.contains("hidden")).toBe(false);
    expect(element("queue-count").textContent).toBe("0");
  });

  it("hides the empty queue copy when jobs are loaded", async () => {
    await mountPopup(() => ({
      ok: true,
      snapshot: makeSnapshot({ jobs: [JOB] }),
    }));
    expect(element("queue-copy").classList.contains("hidden")).toBe(true);
    expect(element("job-list").children).toHaveLength(1);
    expect(element("queue-count").textContent).toBe("1");
  });

  it("styles a successful outcome as success", async () => {
    await mountPopup(() => ({
      ok: true,
      snapshot: makeSnapshot({ lastOutcome: OUTCOME }),
    }));
    const outcome = element("last-outcome");
    expect(outcome.textContent).toContain("Uploaded");
    expect(outcome.classList.contains("notice-success")).toBe(true);
  });

  it("styles a conflict outcome as an error", async () => {
    await mountPopup(() => ({
      ok: true,
      snapshot: makeSnapshot({
        lastOutcome: { ...OUTCOME, status: "permanent_conflict" },
      }),
    }));
    const outcome = element("last-outcome");
    expect(outcome.classList.contains("notice-error")).toBe(true);
    expect(outcome.classList.contains("notice-success")).toBe(false);
  });

  it("styles a retryable outcome as a warning", async () => {
    await mountPopup(() => ({
      ok: true,
      snapshot: makeSnapshot({
        lastOutcome: { ...OUTCOME, status: "retryable_error" },
      }),
    }));
    const outcome = element("last-outcome");
    expect(outcome.classList.contains("notice-warn")).toBe(true);
  });

  it("replaces a stale success style when an action fails", async () => {
    await mountPopup((message) =>
      message.type === "popup_connect"
        ? { ok: false, message: "Local uploader is unavailable" }
        : { ok: true, snapshot: makeSnapshot({ lastOutcome: OUTCOME }) },
    );
    const outcome = element("last-outcome");
    expect(outcome.classList.contains("notice-success")).toBe(true);

    element<HTMLButtonElement>("connect-button").click();
    await settle();
    expect(outcome.textContent).toBe("Local uploader is unavailable");
    expect(outcome.classList.contains("notice-error")).toBe(true);
    expect(outcome.classList.contains("notice-success")).toBe(false);
  });

  it("clears the outcome style when there is no outcome", async () => {
    await mountPopup(() => ({ ok: true, snapshot: makeSnapshot() }));
    const outcome = element("last-outcome");
    expect(outcome.textContent).toBe("");
    expect(outcome.className).toBe("notice notice-info");
  });
});

describe("popup GitHub authorization UX", () => {
  const CHALLENGE = {
    userCode: "ABCD-1234",
    verificationUri: "https://github.com/login/device",
    verificationUriComplete: "https://github.com/login/device?user_code=ABCD-1234",
    expiresAt: "2100-01-01T00:00:00Z",
  };

  function authorizingSnapshot(): ExtensionSnapshot {
    return makeSnapshot({ authState: "authorizing", authorization: CHALLENGE });
  }

  it("auto-opens the durable auth page exactly once per challenge and copies the code", async () => {
    await mountPopup(() => ({ ok: true, snapshot: authorizingSnapshot() }));
    expect(openedTabs).toHaveLength(1);
    const opened = new URL(openedTabs[0]);
    expect(opened.pathname).toContain("auth.html");
    expect(opened.searchParams.get("code")).toBe("ABCD-1234");
    expect(opened.searchParams.get("url")).toContain("github.com/login/device");
    expect(copiedTexts).toEqual(["ABCD-1234"]);

    const link = element<HTMLAnchorElement>("authorization-link");
    expect(link.href).toContain("user_code=ABCD-1234");

    element<HTMLButtonElement>("refresh-button").click();
    await settle();
    expect(openedTabs).toHaveLength(1);
    expect(copiedTexts).toEqual(["ABCD-1234"]);
  });

  it("shows the code card, expiry countdown, and keychain heads-up", async () => {
    await mountPopup(() => ({ ok: true, snapshot: authorizingSnapshot() }));
    expect(element("authorization-code-row").classList.contains("hidden")).toBe(false);
    expect(element("authorization-code").textContent).toBe("ABCD-1234");
    expect(element("authorization-expiry").textContent).toContain("expires in");
    expect(element("authorization-keychain").textContent).toContain("Always Allow");
  });

  it("copies the user code to the clipboard on demand", async () => {
    await mountPopup(() => ({ ok: true, snapshot: authorizingSnapshot() }));
    // The challenge auto-copy already placed the code on the clipboard.
    expect(copiedTexts).toEqual(["ABCD-1234"]);
    element<HTMLButtonElement>("copy-code-button").click();
    await settle();
    expect(copiedTexts).toEqual(["ABCD-1234", "ABCD-1234"]);
    expect(element("copy-code-button").textContent).toBe("Copied");
  });

  it("hides the authorization card and stops the countdown when connected", async () => {
    await mountPopup(() => ({ ok: true, snapshot: makeSnapshot() }));
    expect(element("authorization").classList.contains("hidden")).toBe(true);
    expect(element("authorization-code-row").classList.contains("hidden")).toBe(true);
    expect(element("authorization-expiry").classList.contains("hidden")).toBe(true);
  });

  it("never auto-opens anything when the verification URL is not GitHub", async () => {
    await mountPopup(() => ({
      ok: true,
      snapshot: makeSnapshot({
        authState: "authorizing",
        authorization: {
          ...CHALLENGE,
          verificationUri: "https://example.com/login/device",
          verificationUriComplete: "https://example.com/login/device?user_code=ABCD-1234",
        },
      }),
    }));
    // The auth page still opens so the code stays visible; it must not carry
    // a non-GitHub approval URL.
    expect(openedTabs).toHaveLength(1);
    const opened = new URL(openedTabs[0]);
    expect(opened.pathname).toContain("auth.html");
    expect(opened.searchParams.get("url")).toBeNull();
    expect(element("authorization-link").classList.contains("hidden")).toBe(true);
    expect(element("authorization-code-row").classList.contains("hidden")).toBe(false);
    expect(copiedTexts).toEqual(["ABCD-1234"]);
  });
});

describe("popup queue row naming", () => {
  it("shows the lecture topic as the row headline, never the lectureKey", async () => {
    await mountPopup(() => ({ ok: true, snapshot: makeSnapshot({ jobs: [JOB] }) }));
    const title = element<HTMLElement>("job-list").querySelector(".job-title");
    expect(title?.textContent).toBe("Intro, Smith");
    expect(title?.textContent).not.toContain("EECS 484/2026-fall");
  });

  it("synthesizes the resolved identity when the capture had no topic", async () => {
    await mountPopup(() => ({
      ok: true,
      snapshot: makeSnapshot({
        jobs: [{ ...JOB, displayTitle: null, targetPath: "eecs484/discussions/002.md" }],
      }),
    }));
    const title = element<HTMLElement>("job-list").querySelector(".job-title");
    expect(title?.textContent).toBe("Discussion 2");
  });

  it("shows a human date and a truncated hash in the row meta", async () => {
    await mountPopup(() => ({
      ok: true,
      snapshot: makeSnapshot({
        jobs: [{ ...JOB, remoteContentHash: "f258b922005fbd9a94f3dae78f841a9e5a7573a4c4e3223900bf0e2d1749c423" }],
      }),
    }));
    const meta = element<HTMLElement>("job-list").querySelector(".job-meta");
    expect(meta?.textContent).toContain("Sep 8, 2026");
    expect(meta?.textContent).toContain("f258b922…");
    expect(meta?.textContent).not.toContain("f258b922005fbd9a");
    expect(element<HTMLElement>("job-list").textContent).toContain("Copy hash");
  });
});

describe("popup extension identity", () => {
  it("shows the extension ID and copies it on demand", async () => {
    await mountPopup(() => ({ ok: true, snapshot: makeSnapshot() }));
    expect(element("extension-id").textContent).toBe(
      "ID abcdefghijklmnopqrstuvwxyzabcdef",
    );
    element<HTMLButtonElement>("copy-id-button").click();
    await settle();
    expect(copiedTexts).toEqual(["abcdefghijklmnopqrstuvwxyzabcdef"]);
    expect(element("copy-id-button").textContent).toBe("Copied");
  });
});
