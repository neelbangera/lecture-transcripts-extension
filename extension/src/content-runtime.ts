import type { BackgroundResponse } from "./background";
import type {
  ContentScriptDependencies,
  ContentScriptParser,
  HandoffResult,
  NormalizedTranscriptSnapshot,
  PageSelectors,
  ParserRejectionStatus,
  ParserResult,
} from "./content";
import { COURSE_MAPPINGS, type CourseConfig } from "./course-config";
import {
  extractTranscriptSnapshot,
  hashTranscriptForms,
  parseLecturePage,
  type CourseMapping,
  type OverviewFetcher,
  type SelectorFixture,
} from "./leccap-parser";
import {
  TranscriptJobValidationError,
  createTranscriptJob,
  type TranscriptJob,
} from "./transcript-job";

export const CAPTURE_JOB_MESSAGE_TYPE = "capture_job";
export const DEFAULT_STABLE_SNAPSHOT_COUNT = 2;

export interface ChromeRuntimeMessaging {
  sendMessage(message: unknown, callback: (response: unknown) => void): void;
  lastError?: { message?: string } | undefined;
}

export function adaptCourseMappings(
  configs: readonly CourseConfig[],
): CourseMapping[] {
  return configs.map((config) => ({
    pageCourseText: config.pageCourseText,
    courseName: config.courseName,
    courseSlug: config.courseSlug,
    supportedTerms: [...config.supportedTerms],
  }));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function isBackgroundResponse(
  value: unknown,
): value is BackgroundResponse {
  if (!isRecord(value) || typeof value.ok !== "boolean") return false;
  for (const key of ["status", "errorCategory", "message"]) {
    if (key in value && typeof value[key] !== "string") return false;
  }
  if ("snapshot" in value && !isRecord(value.snapshot)) return false;
  return true;
}

function runtimeFromGlobal(): ChromeRuntimeMessaging | undefined {
  const chromeApi = (
    globalThis as { chrome?: { runtime?: ChromeRuntimeMessaging } }
  ).chrome;
  return chromeApi?.runtime;
}

export function createRuntimeHandoff(
  runtime?: ChromeRuntimeMessaging,
): (job: TranscriptJob) => Promise<HandoffResult> {
  return (job) =>
    new Promise<HandoffResult>((resolve, reject) => {
      const target = runtime ?? runtimeFromGlobal();
      if (!target || typeof target.sendMessage !== "function") {
        reject(new Error("chrome.runtime.sendMessage is unavailable"));
        return;
      }
      try {
        target.sendMessage(
          { type: CAPTURE_JOB_MESSAGE_TYPE, job },
          (response: unknown) => {
            const lastError = target.lastError;
            if (lastError) {
              reject(
                new Error(
                  lastError.message ||
                    "the extension background channel is unavailable",
                ),
              );
              return;
            }
            if (!isBackgroundResponse(response)) {
              reject(
                new Error(
                  "the background response did not match the expected shape",
                ),
              );
              return;
            }
            if (!response.ok) {
              reject(
                new Error(
                  response.message ??
                    response.errorCategory ??
                    response.status ??
                    "the capture handoff was not accepted",
                ),
              );
              return;
            }
            if (
              response.status === "queued" ||
              response.status === "already_queued"
            ) {
              resolve({ status: response.status });
              return;
            }
            resolve({});
          },
        );
      } catch (error) {
        reject(error instanceof Error ? error : new Error(String(error)));
      }
    });
}

function pageOverviewFetcher(document: Document): OverviewFetcher | undefined {
  const view = document.defaultView;
  if (!view || typeof view.fetch !== "function") return undefined;
  return (input, init) => view.fetch(input, init);
}

function redactPath(value: string): string {
  try {
    const url = new URL(value);
    const path = url.pathname.replace(/[0-9a-f]{6,}|\d{3,}/gi, "<id>");
    return `${url.host}${path}`;
  } catch {
    return "<unparseable-url>";
  }
}

/**
 * True when a successful overview fetch returned a page without the recorded
 * recording list (the live server serves a reduced page to bare fetches) and
 * the page is not a sign-in redirect.
 */
export function needsOverviewRender(html: string): boolean {
  if (html.includes('id="recordings"')) return false;
  if (/weblogin|shibboleth|sign in|log in/i.test(html)) return false;
  return true;
}

/**
 * Render the linked overview in a hidden same-origin iframe so the server
 * treats the request like a document navigation, then return the rendered
 * DOM. Returns null when framing is blocked or the page never populates.
 */
export function renderOverviewInIframe(
  document: Document,
  url: string,
  timeoutMs = 12_000,
): Promise<string | null> {
  const view = document.defaultView;
  if (!view) return Promise.resolve(null);
  return new Promise<string | null>((resolve) => {
    const iframe = document.createElement("iframe");
    iframe.setAttribute("aria-hidden", "true");
    iframe.style.position = "absolute";
    iframe.style.width = "1px";
    iframe.style.height = "1px";
    iframe.style.opacity = "0";
    iframe.style.pointerEvents = "none";
    let settled = false;
    const finish = (value: string | null): void => {
      if (settled) return;
      settled = true;
      view.clearTimeout(deadline);
      iframe.remove();
      resolve(value);
    };
    const deadline = view.setTimeout(() => finish(null), timeoutMs);
    const readDocument = (): string | null => {
      try {
        const doc = iframe.contentDocument;
        return doc?.documentElement?.outerHTML ?? null;
      } catch {
        return null;
      }
    };
    iframe.addEventListener("load", () => {
      const started = Date.now();
      const poll = (): void => {
        const html = readDocument();
        if (html && html.includes('id="recordings"')) {
          finish(html);
          return;
        }
        if (Date.now() - started > 4000) {
          finish(html);
          return;
        }
        view.setTimeout(poll, 200);
      };
      poll();
    });
    iframe.src = url;
    (document.body ?? document.documentElement).append(iframe);
  });
}

/**
 * Temporary, sanitized fetch diagnostics: URL path with identifiers redacted,
 * status, size, and page-shape marker booleans. Never logs page content. When
 * a successful fetch lacks the recording list, the linked overview is rendered
 * in a hidden same-origin iframe and the rendered DOM is used instead.
 */
function withOverviewFallback(
  fetcher: OverviewFetcher,
  document: Document,
): OverviewFetcher {
  return async (input, init) => {
    const response = await fetcher(input, init);
    const raw = response as { status?: number; url?: string };
    let html = "";
    try {
      html = await response.text();
    } catch {
      html = "";
    }
    let source = "fetch";
    if (response.ok && needsOverviewRender(html)) {
      const rendered = await renderOverviewInIframe(document, String(input));
      if (rendered && rendered.includes('id="recordings"')) {
        html = rendered;
        source = "iframe";
      }
    }
    const titleMatch = /<title>\s*([^<]{0,80}?)\s*<\/title>/i.exec(html);
    const markers = {
      recordingsId: html.includes('id="recordings"'),
      recordingCard: html.includes('class="recording'),
      signIn: /weblogin|shibboleth|sign in|log in/i.test(html),
      unauthorized: /unauthor|forbidden|not authorized|permission|access denied/i.test(html),
      expired: /expired/i.test(html),
      errorHeading: /<h[12][^>]*>\s*(?:error|not found|oops|problem)/i.test(html),
      appRoot: html.includes('id="root"'),
    };
    console.log(
      `[lecture-transcripts] overview fetch url=${redactPath(String(input))}` +
        ` source=${source}` +
        ` status=${typeof raw.status === "number" ? raw.status : "?"}` +
        ` bytes=${html.length}` +
        ` title=${JSON.stringify(titleMatch ? titleMatch[1] : null)}` +
        ` markers=${JSON.stringify(markers)}`,
    );
    return { ok: response.ok, text: async () => html };
  };
}

function rejectionStatusFromJobError(error: unknown): ParserRejectionStatus {
  if (error instanceof TranscriptJobValidationError) {
    switch (error.code) {
      case "rejected_missing_identity":
      case "rejected_ambiguous_metadata":
      case "rejected_oversized":
      case "rejected_invalid_hash":
      case "rejected_unsafe_url":
        return error.code;
      default:
        return "rejected_ambiguous_metadata";
    }
  }
  return "rejected_ambiguous_metadata";
}

export interface ContentRuntimeParserOptions {
  selectors: SelectorFixture;
  courseMappings?: readonly CourseMapping[];
  fetchOverview?: OverviewFetcher;
}

export function createContentRuntimeParser(
  options: ContentRuntimeParserOptions,
): ContentScriptParser<TranscriptJob> {
  const fixture = options.selectors;
  const courseMappings =
    options.courseMappings ?? adaptCourseMappings(COURSE_MAPPINGS);

  return {
    async snapshot(
      document: Document,
      selectors: PageSelectors,
    ): Promise<ParserResult<NormalizedTranscriptSnapshot>> {
      const extraction = extractTranscriptSnapshot(document, {
        transcriptContainerSelector: selectors.transcriptContainerSelector,
        sanityMinChars: selectors.sanityMinChars,
      });
      if (!extraction.ok) {
        if (extraction.status === "not_ready") {
          return {
            ok: true,
            value: {
              transcript: "",
              timestampedTranscript: "",
              contentHash: await hashTranscriptForms("", ""),
            },
          };
        }
        return { ok: false, status: extraction.status };
      }
      const { transcript, timestampedTranscript } = extraction.value;
      return {
        ok: true,
        value: {
          transcript,
          timestampedTranscript,
          contentHash: await hashTranscriptForms(
            transcript,
            timestampedTranscript,
          ),
        },
      };
    },

    async buildJob(
      document: Document,
      snapshot: NormalizedTranscriptSnapshot,
      capturedAt: string,
    ): Promise<ParserResult<TranscriptJob>> {
      const pageFetcher = pageOverviewFetcher(document);
      const parsed = await parseLecturePage(document, {
        selectors: fixture,
        courseMappings,
        fetchOverview:
          options.fetchOverview ??
          (pageFetcher ? withOverviewFallback(pageFetcher, document) : undefined),
        stableSnapshotCount:
          snapshot.stableSnapshotCount ?? DEFAULT_STABLE_SNAPSHOT_COUNT,
      });
      if (!parsed.supported) {
        // Fixed parser reason text only; never page content or transcript text.
        console.log(
          "[lecture-transcripts] parser rejection:",
          parsed.status,
          parsed.reason,
        );
        return { ok: false, status: parsed.status };
      }
      try {
        const job = createTranscriptJob({
          courseName: parsed.courseName,
          courseSlug: parsed.courseSlug,
          term: parsed.term,
          lectureNumber: parsed.lectureNumber,
          lectureDate: parsed.lectureDate,
          sourceUrl: parsed.sourceUrl,
          capturedAt,
          transcript: parsed.transcript,
          timestampedTranscript: parsed.timestampedTranscript,
        });
        return { ok: true, value: job };
      } catch (error) {
        console.log(
          "[lecture-transcripts] job rejection:",
          rejectionStatusFromJobError(error),
          error instanceof Error ? error.message : "unknown",
        );
        return { ok: false, status: rejectionStatusFromJobError(error) };
      }
    },
  };
}

export function toPageSelectors(fixture: SelectorFixture): PageSelectors {
  return {
    transcriptButtonSelector: fixture.transcriptButtonSelector,
    transcriptContainerSelector: fixture.transcriptContainerSelector,
    completionIndicator: {
      selector: fixture.completionIndicator.selector,
      mode: fixture.completionIndicator.mode,
      attribute: fixture.completionIndicator.attribute,
      valueRegex: fixture.completionIndicator.valueRegex,
      populationSelector: fixture.completionIndicator.populationSelector,
    },
    loadingIndicatorSelector: fixture.loadingIndicatorSelector,
    loadingTextMarkers: [...fixture.loadingTextMarkers],
    stabilityDebounceMs: fixture.stabilityDebounceMs,
    sanityMinChars: fixture.sanityMinChars,
  };
}

export function createProductionContentDependencies(
  fixture: SelectorFixture,
): ContentScriptDependencies<TranscriptJob> {
  return {
    selectors: toPageSelectors(fixture),
    parser: createContentRuntimeParser({
      selectors: fixture,
      courseMappings: adaptCourseMappings(COURSE_MAPPINGS),
    }),
    handoff: createRuntimeHandoff(),
    onStatus: (status) => {
      // Status-only diagnostics; never logs transcript text or page content.
      console.log("[lecture-transcripts] capture status:", status);
    },
  };
}
