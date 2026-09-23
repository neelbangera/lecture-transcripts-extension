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
};

function makeSnapshot(
  options: { jobs?: JobSummary[]; lastOutcome?: LastOutcome | null } = {},
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
    authState: "connected",
    authorization: {
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
  const globals = globalThis as Record<string, unknown>;
  globals.window = dom.window;
  globals.document = dom.window.document;
  globals.chrome = {
    runtime: {
      sendMessage: (message: { type: string }, callback: (response: unknown) => void) => {
        callback(respond(message));
      },
      openOptionsPage: () => undefined,
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
    expect(outcome.className).toBe("notice");
  });
});
