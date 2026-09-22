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
      const parsed = await parseLecturePage(document, {
        selectors: fixture,
        courseMappings,
        fetchOverview: options.fetchOverview ?? pageOverviewFetcher(document),
        stableSnapshotCount:
          snapshot.stableSnapshotCount ?? DEFAULT_STABLE_SNAPSHOT_COUNT,
      });
      if (!parsed.supported) {
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
      console.debug("[lecture-transcripts] capture status:", status);
    },
  };
}
