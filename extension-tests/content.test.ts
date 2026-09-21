import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { JSDOM } from "jsdom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  OBSERVATION_TIMEOUT_MS,
  STABILITY_DEBOUNCE_MS,
  URL_POLL_INTERVAL_MS,
  createContentScript,
  installContentRuntime,
  type ContentScriptController,
  type ContentScriptParser,
  type HandoffResult,
  type NormalizedTranscriptSnapshot,
  type PageCaptureStatus,
  type ParserRejectionStatus,
} from "../extension/src/content";
import {
  CAPTURE_JOB_MESSAGE_TYPE,
  createContentRuntimeParser,
  createRuntimeHandoff,
  isBackgroundResponse,
  toPageSelectors,
  type ChromeRuntimeMessaging,
} from "../extension/src/content-runtime";
import {
  extractTranscriptSnapshot,
  hashTranscriptForms,
  type CourseMapping,
  type SelectorFixture,
} from "../extension/src/leccap-parser";
import {
  createTranscriptJob,
  type TranscriptJob,
} from "../extension/src/transcript-job";

const fixtureDirectory = fileURLToPath(new URL("./fixtures/", import.meta.url));
const selectorsFixture = JSON.parse(
  readFileSync(`${fixtureDirectory}lecture-page.selectors.json`, "utf8"),
) as SelectorFixture;
const courseFixture = JSON.parse(
  readFileSync(`${fixtureDirectory}course-mapping.json`, "utf8"),
) as { courseMappings: CourseMapping[] };
const lectureHtml = readFileSync(`${fixtureDirectory}lecture-page.html`, "utf8");
const nonLectureHtml = readFileSync(
  `${fixtureDirectory}non-lecture-page.html`,
  "utf8",
);
const overviewHtml = readFileSync(
  `${fixtureDirectory}overview-page.html`,
  "utf8",
);
const expected = JSON.parse(
  readFileSync(`${fixtureDirectory}lecture-page.expected.json`, "utf8"),
) as Record<string, unknown>;

const LECTURE_URL = "https://leccap.engin.umich.edu/leccap/player/r/sanitized01";
const OVERVIEW_URL =
  "https://leccap.engin.umich.edu/leccap/site/sanitizedoverview";
const CAPTURED_AT = "2026-09-20T12:00:00Z";
const READY_TRANSCRIPT =
  "A sufficiently long stable transcript body for the capture coordinator test.";

const pageSelectors = toPageSelectors(selectorsFixture);

const realSetImmediate = (
  globalThis as unknown as {
    setImmediate: (callback: () => void) => unknown;
  }
).setImmediate;

async function flushMacrotasks(): Promise<void> {
  for (let attempt = 0; attempt < 5; attempt += 1) {
    await new Promise<void>((resolve) => realSetImmediate(resolve));
  }
}

type BuildJob = ContentScriptParser<TranscriptJob>["buildJob"];
type Handoff = (job: TranscriptJob) => Promise<void>;

const FAKE_JOB: TranscriptJob = createTranscriptJob({
  courseName: "EECS 484",
  courseSlug: "eecs484",
  term: "2026-fall",
  lectureNumber: 1,
  lectureDate: "2026-09-01",
  sourceUrl: LECTURE_URL,
  capturedAt: CAPTURED_AT,
  transcript: READY_TRANSCRIPT,
});

function makeDom(html: string, url = LECTURE_URL): JSDOM {
  return new JSDOM(html, { url });
}

function stubDomGlobals(dom: JSDOM): void {
  const globals = globalThis as Record<string, unknown>;
  globals.Element = dom.window.Element;
  globals.HTMLElement = dom.window.HTMLElement;
  globals.MutationObserver = dom.window.MutationObserver;
}

function makeTestWindow(dom: JSDOM): Window {
  const view = dom.window;
  const timers = globalThis as unknown as {
    setTimeout(handler: () => void, timeout?: number): number;
    clearTimeout(id: number): void;
    setInterval(handler: () => void, timeout?: number): number;
    clearInterval(id: number): void;
  };
  return {
    get location(): Location {
      return view.location;
    },
    setTimeout: (handler: unknown, timeout?: number) =>
      timers.setTimeout(handler as () => void, timeout),
    clearTimeout: (id: number) => timers.clearTimeout(id),
    setInterval: (handler: unknown, timeout?: number) =>
      timers.setInterval(handler as () => void, timeout),
    clearInterval: (id: number) => timers.clearInterval(id),
    addEventListener: view.addEventListener.bind(view),
    removeEventListener: view.removeEventListener.bind(view),
  } as unknown as Window;
}

function startCoordinator(
  dom: JSDOM,
  parser: ContentScriptParser<TranscriptJob>,
  handoff: (
    job: TranscriptJob,
  ) => void | HandoffResult | Promise<void | HandoffResult> = () => ({}),
  statuses: PageCaptureStatus[] = [],
): ContentScriptController {
  stubDomGlobals(dom);
  return createContentScript<TranscriptJob>({
    selectors: pageSelectors,
    parser,
    handoff,
    onStatus: (status) => statuses.push(status),
    now: () => Date.now(),
    document: dom.window.document,
    window: makeTestWindow(dom),
  });
}

function transcriptButton(dom: JSDOM): HTMLElement {
  const button = dom.window.document.querySelector<HTMLElement>(
    "#sourcebar button[title='Hide Transcript'], #sourcebar button[title='Show Transcript']",
  );
  if (!button) throw new Error("fixture transcript button is missing");
  return button;
}

function snapshotOf(
  transcript: string,
  timestampedTranscript = "",
): NormalizedTranscriptSnapshot {
  return {
    transcript,
    timestampedTranscript,
    contentHash: `${transcript}\u0000${timestampedTranscript}`,
  };
}

function fakeParser(options: {
  snapshots?: () => NormalizedTranscriptSnapshot;
  buildJob?: BuildJob;
}): { parser: ContentScriptParser<TranscriptJob>; buildJob: ReturnType<typeof vi.fn<BuildJob>> } {
  const buildJob = options.buildJob
    ? vi.fn<BuildJob>(options.buildJob)
    : vi.fn<BuildJob>(async () => ({ ok: true, value: FAKE_JOB }));
  return {
    parser: {
      async snapshot() {
        return {
          ok: true,
          value: (options.snapshots ?? (() => snapshotOf(READY_TRANSCRIPT)))(),
        };
      },
      buildJob,
    },
    buildJob,
  };
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(CAPTURED_AT));
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  const globals = globalThis as Record<string, unknown>;
  delete globals.Element;
  delete globals.HTMLElement;
  delete globals.MutationObserver;
  delete globals.document;
  delete globals.window;
  delete globals.chrome;
  delete globals.__STAGE0_SELECTORS__;
});

describe("content coordinator activation", () => {
  it("submits nothing on an ordinary page visit", async () => {
    const dom = makeDom(lectureHtml);
    transcriptButton(dom).setAttribute("title", "Show Transcript");
    const { parser, buildJob } = fakeParser({});
    const handoff = vi.fn<Handoff>(async () => {});
    const controller = startCoordinator(dom, parser, handoff);

    await vi.advanceTimersByTimeAsync(
      OBSERVATION_TIMEOUT_MS + URL_POLL_INTERVAL_MS,
    );

    expect(buildJob).not.toHaveBeenCalled();
    expect(handoff).not.toHaveBeenCalled();
    expect(controller.getStatus()).toBe("idle");
    controller.dispose();
  });

  it("starts exactly one capture from a Show Transcript click", async () => {
    const dom = makeDom(lectureHtml);
    const button = transcriptButton(dom);
    button.setAttribute("title", "Show Transcript");
    const { parser, buildJob } = fakeParser({});
    const handoff = vi.fn<Handoff>(async () => {});
    const statuses: PageCaptureStatus[] = [];
    const controller = startCoordinator(dom, parser, handoff, statuses);

    button.dispatchEvent(
      new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }),
    );
    button.setAttribute("title", "Hide Transcript");
    await vi.advanceTimersByTimeAsync(1);
    await vi.advanceTimersByTimeAsync(STABILITY_DEBOUNCE_MS * 2);

    expect(buildJob).toHaveBeenCalledTimes(1);
    expect(buildJob.mock.calls[0][1]).toMatchObject({ stableSnapshotCount: 2 });
    expect(handoff).toHaveBeenCalledTimes(1);
    expect(handoff.mock.calls[0][0]).toBe(FAKE_JOB);
    expect(statuses).toContain("activated");
    expect(controller.getStatus()).toBe("handoff_pending");
    controller.dispose();
  });

  it("starts a capture for an already-expanded populated transcript", async () => {
    const dom = makeDom(lectureHtml);
    const { parser, buildJob } = fakeParser({});
    const handoff = vi.fn<Handoff>(async () => {});
    const controller = startCoordinator(dom, parser, handoff);

    await vi.advanceTimersByTimeAsync(STABILITY_DEBOUNCE_MS * 2);

    expect(buildJob).toHaveBeenCalledTimes(1);
    expect(handoff).toHaveBeenCalledTimes(1);
    expect(controller.getStatus()).toBe("handoff_pending");
    controller.dispose();
  });

  it("starts a URL-change capture only when the transcript is open and populated", async () => {
    const dom = makeDom(lectureHtml);
    const button = transcriptButton(dom);
    button.setAttribute("title", "Show Transcript");
    const { parser, buildJob } = fakeParser({});
    const handoff = vi.fn<Handoff>(async () => {});
    const controller = startCoordinator(dom, parser, handoff);

    dom.window.history.pushState({}, "", `${LECTURE_URL}-two`);
    await vi.advanceTimersByTimeAsync(URL_POLL_INTERVAL_MS);
    expect(buildJob).not.toHaveBeenCalled();

    button.setAttribute("title", "Hide Transcript");
    dom.window.history.pushState({}, "", `${LECTURE_URL}-three`);
    await vi.advanceTimersByTimeAsync(
      URL_POLL_INTERVAL_MS + STABILITY_DEBOUNCE_MS * 2,
    );

    expect(buildJob).toHaveBeenCalledTimes(1);
    expect(handoff).toHaveBeenCalledTimes(1);
    controller.dispose();
  });

  it("restarts the stability window when the transcript mutates", async () => {
    const dom = makeDom(lectureHtml);
    const text = dom.window.document.querySelector(".transcript-text");
    if (!text) throw new Error("fixture transcript text is missing");
    let currentText = READY_TRANSCRIPT;
    const observed: string[] = [];
    const parser: ContentScriptParser<TranscriptJob> = {
      async snapshot() {
        observed.push(currentText);
        return { ok: true, value: snapshotOf(currentText) };
      },
      buildJob: vi.fn<BuildJob>(async () => ({ ok: true, value: FAKE_JOB })),
    };
    const handoff = vi.fn<Handoff>(async () => {});
    const controller = startCoordinator(dom, parser, handoff);

    await vi.advanceTimersByTimeAsync(STABILITY_DEBOUNCE_MS);
    expect(observed).toHaveLength(1);
    expect(parser.buildJob).not.toHaveBeenCalled();

    currentText = `${READY_TRANSCRIPT} Extended.`;
    text.textContent = currentText;
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(STABILITY_DEBOUNCE_MS);
    expect(observed).toHaveLength(2);
    expect(parser.buildJob).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(STABILITY_DEBOUNCE_MS);
    expect(parser.buildJob).toHaveBeenCalledTimes(1);
    expect(handoff).toHaveBeenCalledTimes(1);
    controller.dispose();
  });

  it("ends as not_ready when the observation window times out", async () => {
    const dom = makeDom(lectureHtml);
    const button = transcriptButton(dom);
    button.setAttribute("title", "Show Transcript");
    const { parser, buildJob } = fakeParser({});
    const handoff = vi.fn<Handoff>(async () => {});
    const statuses: PageCaptureStatus[] = [];
    const controller = startCoordinator(dom, parser, handoff, statuses);

    button.dispatchEvent(
      new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }),
    );
    await vi.advanceTimersByTimeAsync(OBSERVATION_TIMEOUT_MS + 1);

    expect(controller.getStatus()).toBe("not_ready");
    expect(statuses).toContain("not_ready");
    expect(buildJob).not.toHaveBeenCalled();
    controller.dispose();
  });

  const rejectionStatuses: ParserRejectionStatus[] = [
    "rejected_missing_identity",
    "rejected_ambiguous_metadata",
    "rejected_oversized",
    "rejected_invalid_hash",
    "rejected_unsafe_url",
  ];

  for (const status of rejectionStatuses) {
    it(`surfaces the parser rejection status ${status}`, async () => {
      const dom = makeDom(lectureHtml);
      const { parser } = fakeParser({
        buildJob: async () => ({ ok: false, status }),
      });
      const handoff = vi.fn<Handoff>(async () => {});
      const controller = startCoordinator(dom, parser, handoff);

      await vi.advanceTimersByTimeAsync(STABILITY_DEBOUNCE_MS * 2);

      expect(controller.getStatus()).toBe(status);
      expect(handoff).not.toHaveBeenCalled();
      controller.dispose();
    });
  }

  it("maps a parser not_ready build result to the not_ready page status", async () => {
    const dom = makeDom(lectureHtml);
    const { parser } = fakeParser({
      buildJob: async () => ({ ok: false, status: "not_ready" }),
    });
    const handoff = vi.fn<Handoff>(async () => {});
    const controller = startCoordinator(dom, parser, handoff);

    await vi.advanceTimersByTimeAsync(STABILITY_DEBOUNCE_MS * 2);

    expect(controller.getStatus()).toBe("not_ready");
    expect(handoff).not.toHaveBeenCalled();
    controller.dispose();
  });

  it("keeps handoff_pending when the handoff rejects", async () => {
    const dom = makeDom(lectureHtml);
    const { parser, buildJob } = fakeParser({});
    const handoff = vi.fn<Handoff>(async () => {
      throw new Error("background channel closed");
    });
    const controller = startCoordinator(dom, parser, handoff);

    await vi.advanceTimersByTimeAsync(STABILITY_DEBOUNCE_MS * 2);
    expect(buildJob).toHaveBeenCalledTimes(1);
    expect(handoff).toHaveBeenCalledTimes(1);
    expect(controller.getStatus()).toBe("handoff_pending");

    await vi.advanceTimersByTimeAsync(OBSERVATION_TIMEOUT_MS);
    expect(controller.getStatus()).toBe("handoff_pending");
    expect(buildJob).toHaveBeenCalledTimes(1);
    controller.dispose();
  });

  it("keeps handoff_pending when chrome.runtime.lastError reports a missing channel", async () => {
    const dom = makeDom(lectureHtml);
    const runtime: ChromeRuntimeMessaging = {
      lastError: undefined,
      sendMessage(_message, callback) {
        runtime.lastError = { message: "Receiving end does not exist." };
        callback(undefined);
        runtime.lastError = undefined;
      },
    };
    const { parser } = fakeParser({});
    const controller = startCoordinator(dom, parser, createRuntimeHandoff(runtime));

    await vi.advanceTimersByTimeAsync(STABILITY_DEBOUNCE_MS * 2);

    expect(controller.getStatus()).toBe("handoff_pending");
    controller.dispose();
  });

  it("runs the real parser pipeline and sends a validated capture_job", async () => {
    const dom = makeDom(lectureHtml);
    const fetchCalls: string[] = [];
    const parser = createContentRuntimeParser({
      selectors: selectorsFixture,
      courseMappings: courseFixture.courseMappings,
      fetchOverview: async (url) => {
        fetchCalls.push(url);
        return { ok: true, text: async () => overviewHtml };
      },
    });
    const sent: unknown[] = [];
    const runtime: ChromeRuntimeMessaging = {
      sendMessage(message, callback) {
        sent.push(message);
        callback({ ok: true, status: "queued" });
      },
    };
    const statuses: PageCaptureStatus[] = [];
    const controller = startCoordinator(
      dom,
      parser,
      createRuntimeHandoff(runtime),
      statuses,
    );

    await vi.advanceTimersByTimeAsync(STABILITY_DEBOUNCE_MS);
    await flushMacrotasks();
    await vi.advanceTimersByTimeAsync(STABILITY_DEBOUNCE_MS);
    await flushMacrotasks();
    await vi.advanceTimersByTimeAsync(100);
    await flushMacrotasks();

    expect(controller.getStatus()).toBe("handoff_pending");
    expect(fetchCalls).toEqual([OVERVIEW_URL]);
    expect(sent).toHaveLength(1);
    const message = sent[0] as { type: string; job: TranscriptJob };
    expect(message.type).toBe(CAPTURE_JOB_MESSAGE_TYPE);
    expect(message.job).toMatchObject({
      schemaVersion: 1,
      courseName: "EECS 484",
      courseSlug: "eecs484",
      term: "2026-fall",
      lectureNumber: 1,
      lectureDate: "2026-09-01",
      sourceUrl: LECTURE_URL,
      capturedAt: expect.stringMatching(/^2026-09-20T12:00:\d{2}Z$/),
      transcript: expected.transcript,
      timestampedTranscript: expected.timestampedTranscript,
      lectureKey: expected.lectureKey,
      contentHash: expected.contentHash,
    });
    controller.dispose();
  });
});

describe("content runtime parser adapter", () => {
  function runtimeParser(overrides: {
    fetchOverview?: (url: string) => Promise<{ ok: boolean; text: () => Promise<string> }>;
    courseMappings?: readonly CourseMapping[];
  } = {}) {
    return createContentRuntimeParser({
      selectors: selectorsFixture,
      courseMappings: overrides.courseMappings ?? courseFixture.courseMappings,
      fetchOverview:
        overrides.fetchOverview ??
        (async () => ({ ok: true, text: async () => overviewHtml })),
    });
  }

  it("extracts only the transcript container forms and hashes them", async () => {
    const dom = makeDom(lectureHtml);
    const result = await runtimeParser().snapshot(
      dom.window.document,
      pageSelectors,
    );

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.value.transcript).toBe(expected.transcript);
    expect(result.value.timestampedTranscript).toBe(
      expected.timestampedTranscript,
    );
    expect(result.value.contentHash).toBe(expected.contentHash);
    expect(result.value.contentHash).toBe(
      await hashTranscriptForms(
        expected.transcript as string,
        expected.timestampedTranscript as string,
      ),
    );
  });

  it("returns an empty stable read when the transcript rows are not present", async () => {
    const dom = makeDom(lectureHtml);
    dom.window.document.querySelector(".transcript-scroller")?.remove();
    const result = await runtimeParser().snapshot(
      dom.window.document,
      pageSelectors,
    );

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.value.transcript).toBe("");
    expect(result.value.timestampedTranscript).toBe("");
    expect(result.value.contentHash).toBe(await hashTranscriptForms("", ""));
  });

  it("rejects a mixed timestamp shape from the snapshot", async () => {
    const dom = makeDom(lectureHtml);
    dom.window.document.querySelector(".transcript-time")?.remove();
    const result = await runtimeParser().snapshot(
      dom.window.document,
      pageSelectors,
    );

    expect(result).toMatchObject({
      ok: false,
      status: "rejected_ambiguous_metadata",
    });
  });

  it("does not fetch the overview during a snapshot", async () => {
    const dom = makeDom(lectureHtml);
    const fetchCalls: string[] = [];
    const parser = runtimeParser({
      fetchOverview: async (url) => {
        fetchCalls.push(url);
        return { ok: true, text: async () => overviewHtml };
      },
    });

    await parser.snapshot(dom.window.document, pageSelectors);
    expect(fetchCalls).toEqual([]);
  });

  it("binds the overview fetch to the page window and builds a valid job", async () => {
    const dom = makeDom(lectureHtml);
    const calls: Array<{ url: string; init?: RequestInit }> = [];
    (dom.window as unknown as { fetch: unknown }).fetch = function (
      this: unknown,
      url: string,
      init?: RequestInit,
    ) {
      if (this !== dom.window) throw new Error("overview fetch was not bound");
      calls.push({ url, init });
      return Promise.resolve({ ok: true, text: async () => overviewHtml });
    };
    const parser = createContentRuntimeParser({
      selectors: selectorsFixture,
      courseMappings: courseFixture.courseMappings,
    });

    const snapshot = await parser.snapshot(dom.window.document, pageSelectors);
    expect(snapshot.ok).toBe(true);
    if (!snapshot.ok) return;
    expect(calls).toEqual([]);

    const result = await parser.buildJob(
      dom.window.document,
      { ...snapshot.value, stableSnapshotCount: 2 },
      CAPTURED_AT,
    );

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe(OVERVIEW_URL);
    expect(calls[0].init).toMatchObject({ credentials: "same-origin" });
    expect(result.value).toMatchObject({
      courseName: expected.courseName,
      courseSlug: expected.courseSlug,
      term: expected.term,
      lectureNumber: expected.lectureNumber,
      lectureDate: expected.lectureDate,
      sourceUrl: expected.sourceUrl,
      transcript: expected.transcript,
      timestampedTranscript: expected.timestampedTranscript,
      lectureKey: expected.lectureKey,
      contentHash: expected.contentHash,
    });
  });

  it("defaults the stable snapshot count to two when the run omits it", async () => {
    const dom = makeDom(lectureHtml);
    const parser = runtimeParser();
    const snapshot = await parser.snapshot(dom.window.document, pageSelectors);
    if (!snapshot.ok) throw new Error("fixture snapshot failed");

    const result = await parser.buildJob(
      dom.window.document,
      snapshot.value,
      CAPTURED_AT,
    );

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.value.capturedAt).toBe(CAPTURED_AT);
  });

  it("maps real parser rejections to content statuses", async () => {
    const nonLectureDom = makeDom(nonLectureHtml);
    const nonLecture = await runtimeParser().buildJob(
      nonLectureDom.window.document,
      snapshotOf(READY_TRANSCRIPT),
      CAPTURED_AT,
    );
    expect(nonLecture).toMatchObject({
      ok: false,
      status: "rejected_missing_identity",
    });

    const unmappedDom = makeDom(
      lectureHtml.replace(
        "<span>EECS 484 - Fall 2026</span>",
        "<span>EECS 485 - Fall 2026</span>",
      ),
    );
    const unmapped = await runtimeParser().buildJob(
      unmappedDom.window.document,
      snapshotOf(READY_TRANSCRIPT),
      CAPTURED_AT,
    );
    expect(unmapped).toMatchObject({
      ok: false,
      status: "rejected_ambiguous_metadata",
    });

    const unsafeDom = makeDom(lectureHtml, "https://example.invalid/player");
    const unsafe = await runtimeParser().buildJob(
      unsafeDom.window.document,
      snapshotOf(READY_TRANSCRIPT),
      CAPTURED_AT,
    );
    expect(unsafe).toMatchObject({
      ok: false,
      status: "rejected_unsafe_url",
    });
  });

  it("extracts the fixture forms through the additive snapshot export", () => {
    const dom = makeDom(lectureHtml);
    const result = extractTranscriptSnapshot(dom.window.document, {
      transcriptContainerSelector: selectorsFixture.transcriptContainerSelector,
      sanityMinChars: selectorsFixture.sanityMinChars,
    });

    expect(result).toMatchObject({
      ok: true,
      value: {
        transcript: expected.transcript,
        timestampedTranscript: expected.timestampedTranscript,
        derivedFrom: expected.derivedFrom,
      },
    });
  });
});

describe("chrome runtime handoff adapter", () => {
  it("sends the capture_job message and resolves only for an accepted response", async () => {
    const sent: unknown[] = [];
    const runtime: ChromeRuntimeMessaging = {
      sendMessage(message, callback) {
        sent.push(message);
        callback({ ok: true, status: "queued" });
      },
    };

    await expect(createRuntimeHandoff(runtime)(FAKE_JOB)).resolves.toEqual({
      status: "queued",
    });
    expect(sent).toEqual([
      { type: CAPTURE_JOB_MESSAGE_TYPE, job: FAKE_JOB },
    ]);
  });

  it("surfaces a missing background channel as a thrown error", async () => {
    const runtime: ChromeRuntimeMessaging = {
      lastError: undefined,
      sendMessage(_message, callback) {
        runtime.lastError = { message: "Receiving end does not exist." };
        callback(undefined);
        runtime.lastError = undefined;
      },
    };

    await expect(createRuntimeHandoff(runtime)(FAKE_JOB)).rejects.toThrow(
      "Receiving end does not exist.",
    );
  });

  it("rejects a non-durable background response", async () => {
    const runtime: ChromeRuntimeMessaging = {
      sendMessage(_message, callback) {
        callback({
          ok: false,
          status: "waiting_for_uploader",
          message: "Waiting for local uploader",
        });
      },
    };

    await expect(createRuntimeHandoff(runtime)(FAKE_JOB)).rejects.toThrow(
      "Waiting for local uploader",
    );
  });

  it("rejects an unrecognized background response shape", async () => {
    const runtime = {
      sendMessage: (_message: unknown, callback: (response: unknown) => void) =>
        callback({ nonsense: true }),
    };

    await expect(createRuntimeHandoff(runtime)(FAKE_JOB)).rejects.toThrow(
      "expected shape",
    );
    expect(isBackgroundResponse({ ok: true, status: "queued" })).toBe(true);
    expect(isBackgroundResponse({ nonsense: true })).toBe(false);
  });

  it("rejects when sendMessage is unavailable", async () => {
    await expect(
      createRuntimeHandoff({} as ChromeRuntimeMessaging)(FAKE_JOB),
    ).rejects.toThrow("unavailable");
  });
});

describe("production bootstrap", () => {
  it("does not install the runtime outside a Chrome page context", () => {
    expect(installContentRuntime()).toBeNull();
  });

  it("installs once when the page globals and Stage 0 selectors are present", () => {
    const dom = makeDom(lectureHtml);
    stubDomGlobals(dom);
    const sendMessage = vi.fn(
      (_message: unknown, callback: (response: unknown) => void) => {
        callback({ ok: true, status: "queued" });
      },
    );
    const globals = globalThis as Record<string, unknown>;
    globals.document = dom.window.document;
    globals.window = dom.window;
    globals.chrome = { runtime: { sendMessage } };
    globals.__STAGE0_SELECTORS__ = selectorsFixture;

    const controller = installContentRuntime();
    expect(controller).not.toBeNull();
    expect(installContentRuntime()).toBeNull();
    controller?.dispose();
  });
});
