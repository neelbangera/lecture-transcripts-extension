/**
 * Page-facing transcript capture coordinator.
 *
 * This module deliberately does not know the parser/job module's concrete
 * exports or the service worker's private message shape.  Those boundaries
 * are supplied through ContentScriptDependencies so this file cannot bypass
 * the parser, send arbitrary DOM data, or silently invent a handoff protocol.
 */

import { createProductionContentDependencies } from "./content-runtime";
import type { SelectorFixture } from "./leccap-parser";

export const STABILITY_DEBOUNCE_MS = 1500;
export const OBSERVATION_TIMEOUT_MS = 30_000;
export const URL_POLL_INTERVAL_MS = 1000;

declare const __STAGE0_SELECTORS__: SelectorFixture;

export type ActivationSource = 'click' | 'already-expanded' | 'url-change' | 'page-load';

export type PageCaptureStatus =
  | 'idle'
  | 'activated'
  | 'waiting_for_transcript'
  | 'ready'
  | 'handoff_pending'
  | 'not_ready'
  | 'rejected_missing_identity'
  | 'rejected_ambiguous_metadata'
  | 'rejected_oversized'
  | 'rejected_invalid_hash'
  | 'rejected_unsafe_url';

export interface PageSelectors {
  transcriptButtonSelector: string;
  transcriptContainerSelector: string;
  completionIndicator: {
    selector: string;
    mode: 'present' | 'attribute_equals' | 'text_matches';
    attribute: string | null;
    valueRegex: string | null;
    populationSelector: string | null;
  };
  loadingIndicatorSelector: string | null;
  loadingTextMarkers: string[];
  stabilityDebounceMs: number;
  sanityMinChars: number;
}

export interface NormalizedTranscriptSnapshot {
  /** Normalized plain transcript used for the minimum-size sanity check. */
  transcript: string;
  /** Normalized timestamp-preserving transcript, possibly empty. */
  timestampedTranscript: string;
  /** Framed SHA-256 hash over both normalized forms. */
  contentHash: string;
  /** Identical stable reads observed by the coordinator when it promotes a snapshot. */
  stableSnapshotCount?: number;
}

export type ParserRejectionStatus =
  | 'not_ready'
  | 'rejected_missing_identity'
  | 'rejected_ambiguous_metadata'
  | 'rejected_oversized'
  | 'rejected_invalid_hash'
  | 'rejected_unsafe_url';

export type ParserResult<T> =
  | { ok: true; value: T }
  | { ok: false; status: ParserRejectionStatus };

/**
 * The parser/job layer owns DOM extraction, normalization, date lookup,
 * canonical URL handling, and schema-shaped TranscriptJob construction.
 *
 * `snapshot` is intentionally separate from `buildJob`: the observer may
 * need to compare several normalized reads before metadata/date resolution
 * and handoff are attempted.
 */
export interface ContentScriptParser<Job> {
  snapshot(
    document: Document,
    selectors: PageSelectors,
  ): ParserResult<NormalizedTranscriptSnapshot> | Promise<ParserResult<NormalizedTranscriptSnapshot>>;
  buildJob(
    document: Document,
    snapshot: NormalizedTranscriptSnapshot,
    capturedAt: string,
  ): ParserResult<Job> | Promise<ParserResult<Job>>;
}

export interface HandoffResult {
  /** The handoff adapter may return the correlated uploader acknowledgement. */
  status?: 'queued' | 'already_queued';
}

export interface ContentScriptDependencies<Job> {
  selectors: PageSelectors;
  parser: ContentScriptParser<Job>;
  /**
   * The service-worker adapter is responsible for the bounded outbox and for
   * sending the exact extension-internal message.  It must not resolve as
   * durable until the background layer has received a definitive ack.
   */
  handoff(job: Job): HandoffResult | void | Promise<HandoffResult | void>;
  onStatus?: (status: PageCaptureStatus) => void;
  now?: () => number;
  document?: Document;
  window?: Window;
}

export interface ContentScriptController {
  dispose(): void;
  getStatus(): PageCaptureStatus;
  getLastUrl(): string;
}

interface CaptureRun {
  generation: number;
  deadline: number;
  source: ActivationSource;
  observer: MutationObserver | null;
  timeoutId: number | null;
  attachRetryId: number | null;
  stabilityId: number | null;
  firstSnapshot: NormalizedTranscriptSnapshot | null;
  stableSnapshotCount: number;
  mutationVersion: number;
  handoffStarted: boolean;
  terminal: boolean;
}

const TERMINAL_PARSER_REJECTIONS = new Set<ParserRejectionStatus>([
  'rejected_missing_identity',
  'rejected_ambiguous_metadata',
  'rejected_oversized',
  'rejected_invalid_hash',
  'rejected_unsafe_url',
]);

function isElement(value: EventTarget | Node | null): value is Element {
  return value instanceof Element;
}

function asElement(value: EventTarget | null): Element | null {
  return isElement(value) ? value : null;
}

function normalizedText(value: string | null | undefined): string {
  return (value ?? '').replace(/\s+/gu, ' ').trim();
}

function hasEnoughText(value: string, minimum: number): boolean {
  let count = 0;
  for (const character of value) {
    if (/\S/u.test(character)) count += 1;
  }
  return count >= minimum;
}

function isVisible(element: Element): boolean {
  if (element instanceof HTMLElement) {
    if (element.hidden) return false;
    if (element.getAttribute('aria-hidden') === 'true') return false;
  }

  const view = element.ownerDocument.defaultView;
  if (!view) return true;

  const style = view.getComputedStyle(element);
  return (
    style.display !== 'none' &&
    style.visibility !== 'hidden' &&
    style.visibility !== 'collapse' &&
    style.opacity !== '0'
  );
}

function queryAllWithin(
  document: Document,
  container: Element,
  selector: string,
): Element[] {
  let matches: Element[];
  try {
    matches = Array.from(document.querySelectorAll(selector));
  } catch {
    return [];
  }

  return matches.filter((element) => container.contains(element));
}

function queryFirst(document: Document, selector: string): Element | null {
  try {
    return document.querySelector(selector);
  } catch {
    return null;
  }
}

function matchesRegex(value: string, pattern: string): boolean {
  try {
    return new RegExp(pattern, 'u').test(value);
  } catch {
    return false;
  }
}

function findTranscriptButton(
  document: Document,
  selectors: PageSelectors,
): Element | null {
  const button = queryFirst(document, selectors.transcriptButtonSelector);
  return button && isVisible(button) ? button : null;
}

function findTranscriptContainer(
  document: Document,
  selectors: PageSelectors,
): Element | null {
  const container = queryFirst(document, selectors.transcriptContainerSelector);
  return container && isVisible(container) ? container : null;
}

function hasPopulatedTranscript(
  document: Document,
  container: Element,
  selectors: PageSelectors,
): boolean {
  const selector = selectors.completionIndicator.populationSelector;
  if (!selector) return false;

  return queryAllWithin(document, container, selector).some((element) => {
    return isVisible(element) && Boolean(element.textContent?.trim());
  });
}

function isLoadingRegionActive(
  document: Document,
  container: Element,
  selectors: PageSelectors,
): boolean {
  const selector = selectors.loadingIndicatorSelector;
  if (selector === null) return false;

  const markers = selectors.loadingTextMarkers
    .map((marker) => normalizedText(marker).toLocaleLowerCase())
    .filter(Boolean);

  return queryAllWithin(document, container, selector).some((element) => {
    if (!isVisible(element)) return false;
    const text = normalizedText(element.textContent).toLocaleLowerCase();
    return markers.some((marker) => text.includes(marker));
  });
}

function completionIndicatorMatches(
  document: Document,
  selectors: PageSelectors,
): boolean {
  const indicator = selectors.completionIndicator;
  let elements: Element[];
  try {
    elements = Array.from(document.querySelectorAll(indicator.selector));
  } catch {
    return false;
  }

  return elements.some((element) => {
    if (!isVisible(element)) return false;
    if (indicator.mode === 'present') return true;

    if (indicator.mode === 'attribute_equals') {
      if (!indicator.attribute || !indicator.valueRegex) return false;
      const value = element.getAttribute(indicator.attribute);
      return value !== null && matchesRegex(value, indicator.valueRegex);
    }

    if (indicator.mode === 'text_matches') {
      if (indicator.attribute !== null || !indicator.valueRegex) return false;
      return matchesRegex(normalizedText(element.textContent), indicator.valueRegex);
    }

    return false;
  });
}

function completionIsReady(
  document: Document,
  container: Element,
  selectors: PageSelectors,
): boolean {
  return (
    completionIndicatorMatches(document, selectors) &&
    hasPopulatedTranscript(document, container, selectors) &&
    !isLoadingRegionActive(document, container, selectors)
  );
}

function captureTimestamp(now: () => number): string {
  return new Date(now()).toISOString().replace(/\.\d{3}Z$/u, 'Z');
}

function callStatus(
  callback: ((status: PageCaptureStatus) => void) | undefined,
  status: PageCaptureStatus,
): void {
  callback?.(status);
}

/**
 * Installs the content-script coordinator in the current Leccap document.
 *
 * Normal page visits only install listeners.  A capture run is created only
 * by an opening transcript-control click or by an already-visible,
 * already-expanded, populated transcript observed at startup/URL change.
 */
export function createContentScript<Job>(
  dependencies: ContentScriptDependencies<Job>,
): ContentScriptController {
  const pageDocument = dependencies.document ?? document;
  const pageWindow = dependencies.window ?? window;
  const selectors = dependencies.selectors;
  const now = dependencies.now ?? (() => Date.now());

  let lastUrl = pageWindow.location.href;
  let status: PageCaptureStatus = 'idle';
  let generation = 0;
  let run: CaptureRun | null = null;
  let urlPollId: number | null = null;
  let disposed = false;

  const debounceMs = selectors.stabilityDebounceMs || STABILITY_DEBOUNCE_MS;
  const sanityMinChars = selectors.sanityMinChars || 50;

  const setStatus = (next: PageCaptureStatus): void => {
    if (status === next) return;
    status = next;
    callStatus(dependencies.onStatus, next);
  };

  const clearTimer = (id: number | null): void => {
    if (id !== null) pageWindow.clearTimeout(id);
  };

  const disconnectRun = (capture: CaptureRun): void => {
    capture.observer?.disconnect();
    capture.observer = null;
    clearTimer(capture.timeoutId);
    clearTimer(capture.attachRetryId);
    clearTimer(capture.stabilityId);
    capture.timeoutId = null;
    capture.attachRetryId = null;
    capture.stabilityId = null;
  };

  const resetCapture = (nextStatus: PageCaptureStatus = 'idle'): void => {
    generation += 1;
    if (run) disconnectRun(run);
    run = null;
    setStatus(nextStatus);
  };

  const failNotReady = (capture: CaptureRun): void => {
    if (disposed || run !== capture || capture.terminal) return;
    // Once a stable snapshot has been promoted and job construction or the
    // handoff has begun, the observation timeout must not override the
    // handoff_pending state with a false not_ready result.
    if (capture.handoffStarted) return;
    capture.terminal = true;
    disconnectRun(capture);
    setStatus('not_ready');
  };

  const failWithParserStatus = (
    capture: CaptureRun,
    parserStatus: ParserRejectionStatus,
  ): void => {
    if (disposed || run !== capture || capture.terminal) return;
    capture.terminal = true;
    disconnectRun(capture);
    setStatus(TERMINAL_PARSER_REJECTIONS.has(parserStatus) ? parserStatus : 'not_ready');
  };

  const scheduleStabilityCheck = (capture: CaptureRun): void => {
    if (disposed || run !== capture || capture.terminal) return;
    clearTimer(capture.stabilityId);
    capture.stabilityId = pageWindow.setTimeout(() => {
      capture.stabilityId = null;
      void takeStableSnapshot(capture);
    }, debounceMs);
  };

  const handleParserSnapshot = async (
    capture: CaptureRun,
    snapshotResult: ParserResult<NormalizedTranscriptSnapshot>,
    observedMutationVersion: number,
  ): Promise<void> => {
    if (
      disposed ||
      run !== capture ||
      capture.terminal ||
      capture.mutationVersion !== observedMutationVersion
    ) {
      return;
    }

    if (!snapshotResult.ok) {
      failWithParserStatus(capture, snapshotResult.status);
      return;
    }

    const snapshot = snapshotResult.value;
    if (!hasEnoughText(snapshot.transcript, sanityMinChars)) {
      setStatus('waiting_for_transcript');
      scheduleStabilityCheck(capture);
      return;
    }

    if (!capture.firstSnapshot) {
      capture.firstSnapshot = snapshot;
      capture.stableSnapshotCount = 1;
      scheduleStabilityCheck(capture);
      return;
    }

    if (capture.firstSnapshot.contentHash !== snapshot.contentHash) {
      // A changing transcript is ordinary while rows are rendering.  The
      // later snapshot is not promoted to a stable first read; restart the
      // complete quiet interval from this point.
      capture.firstSnapshot = null;
      capture.stableSnapshotCount = 0;
      scheduleStabilityCheck(capture);
      return;
    }

    capture.stableSnapshotCount += 1;
    setStatus('ready');
    if (capture.handoffStarted) return;
    capture.handoffStarted = true;

    const jobResult = await dependencies.parser.buildJob(
      pageDocument,
      { ...snapshot, stableSnapshotCount: capture.stableSnapshotCount },
      captureTimestamp(now),
    );

    if (disposed || run !== capture || capture.terminal) return;

    if (!jobResult.ok) {
      failWithParserStatus(capture, jobResult.status);
      return;
    }

    // The content script never claims that a job is durably queued.  The
    // adapter owns the bounded outbox and resolves only on a definitive ack.
    setStatus('handoff_pending');
    try {
      await dependencies.handoff(jobResult.value);
    } catch {
      // Keep the state as handoff_pending: the background layer may have
      // retained the job in its outbox even though this call lost its reply.
      // It is unsafe for the page script to report a false rejection here.
      return;
    }

    if (disposed || run !== capture || capture.terminal) return;
    capture.terminal = true;
    disconnectRun(capture);
  };

  const takeStableSnapshot = async (capture: CaptureRun): Promise<void> => {
    if (disposed || run !== capture || capture.terminal) return;
    if (now() >= capture.deadline) {
      failNotReady(capture);
      return;
    }

    const container = findTranscriptContainer(pageDocument, selectors);
    if (!container || !completionIsReady(pageDocument, container, selectors)) {
      setStatus('waiting_for_transcript');
      scheduleStabilityCheck(capture);
      return;
    }

    const observedMutationVersion = capture.mutationVersion;
    const snapshotResult = await dependencies.parser.snapshot(pageDocument, selectors);
    await handleParserSnapshot(capture, snapshotResult, observedMutationVersion);
  };

  const onContainerMutation = (capture: CaptureRun): void => {
    if (disposed || run !== capture || capture.terminal) return;
    capture.mutationVersion += 1;
    capture.firstSnapshot = null;
    capture.stableSnapshotCount = 0;
    clearTimer(capture.stabilityId);
    capture.stabilityId = null;
    setStatus('waiting_for_transcript');

    const container = findTranscriptContainer(pageDocument, selectors);
    if (container && completionIsReady(pageDocument, container, selectors)) {
      scheduleStabilityCheck(capture);
    }
  };

  const attachObserver = (capture: CaptureRun): void => {
    if (disposed || run !== capture || capture.terminal) return;

    const container = findTranscriptContainer(pageDocument, selectors);
    if (!container) {
      if (now() >= capture.deadline) {
        failNotReady(capture);
        return;
      }
      capture.attachRetryId = pageWindow.setTimeout(() => {
        capture.attachRetryId = null;
        attachObserver(capture);
      }, 100);
      return;
    }

    // This is the only MutationObserver created by the content script.  It
    // is deliberately attached to the transcript container, never document,
    // the root app, or the video-player subtree.
    capture.observer = new MutationObserver(() => onContainerMutation(capture));
    capture.observer.observe(container, {
      subtree: true,
      childList: true,
      characterData: true,
      attributes: true,
    });

    setStatus('waiting_for_transcript');
    if (completionIsReady(pageDocument, container, selectors)) {
      scheduleStabilityCheck(capture);
    }
  };

  const startCapture = (source: ActivationSource): void => {
    if (disposed) return;

    if (run && !run.terminal) {
      // One activation produces one observation window.  A second event while
      // that window is alive is not a second capture trigger.
      return;
    }

    if (run) disconnectRun(run);
    generation += 1;
    const capture: CaptureRun = {
      generation,
      deadline: now() + OBSERVATION_TIMEOUT_MS,
      source,
      observer: null,
      timeoutId: null,
      attachRetryId: null,
      stabilityId: null,
      firstSnapshot: null,
      stableSnapshotCount: 0,
      mutationVersion: 0,
      handoffStarted: false,
      terminal: false,
    };
    run = capture;
    setStatus('activated');
    capture.timeoutId = pageWindow.setTimeout(() => failNotReady(capture), OBSERVATION_TIMEOUT_MS);
    attachObserver(capture);
  };

  const alreadyExpandedAndPopulated = (): boolean => {
    const button = findTranscriptButton(pageDocument, selectors);
    const container = findTranscriptContainer(pageDocument, selectors);
    if (!button || !container || !hasPopulatedTranscript(pageDocument, container, selectors)) {
      return false;
    }

    // The observed completion contract uses the exact open-state title
    // `Hide Transcript`; no URL/page-visit heuristic is used here.
    return button.getAttribute('title') === 'Hide Transcript';
  };

  const onTranscriptClick = (event: MouseEvent): void => {
    const target = asElement(event.target);
    const button = target?.closest(selectors.transcriptButtonSelector);
    if (!button || !pageDocument.documentElement.contains(button)) return;

    const titleAtActivation = button.getAttribute('title');
    if (titleAtActivation !== 'Show Transcript') {
      // Clicking the open control closes the transcript.  It cancels an
      // unfinished run but never starts a capture by itself.
      if (titleAtActivation === 'Hide Transcript' && run && !run.handoffStarted) {
        resetCapture('idle');
      }
      return;
    }

    // Let the page apply its expansion first; the observer itself remains
    // scoped to the container and will wait for the rows to render.
    pageWindow.setTimeout(() => startCapture('click'), 0);
  };

  const autoActivate = (source: ActivationSource): void => {
    if (disposed) return;
    if (alreadyExpandedAndPopulated()) {
      startCapture(source);
      return;
    }

    // The transcript viewer is not rendered until the control is used, so a
    // recognized lecture page activates capture by opening the transcript
    // itself. The synthetic click also flows through the normal document
    // listener, which keeps one activation path for user and automatic runs.
    const button = findTranscriptButton(pageDocument, selectors);
    if (!button || button.getAttribute('title') !== 'Show Transcript') return;
    pageWindow.setTimeout(() => {
      if (disposed) return;
      const clickable = button as Partial<HTMLElement>;
      if (typeof clickable.click === 'function') {
        clickable.click();
      }
    }, 0);
  };

  const onUrlChange = (): void => {
    const nextUrl = pageWindow.location.href;
    if (nextUrl === lastUrl) return;
    lastUrl = nextUrl;
    resetCapture('idle');

    // A URL change is a reset, then the same automatic activation rule as a
    // fresh page load: only a recognized lecture page opens and captures.
    autoActivate('url-change');
  };

  const onPopState = (): void => onUrlChange();
  pageDocument.addEventListener('click', onTranscriptClick, true);
  pageWindow.addEventListener('popstate', onPopState);
  urlPollId = pageWindow.setInterval(onUrlChange, URL_POLL_INTERVAL_MS);

  // A recognized lecture page captures on load: an already-open transcript is
  // captured directly, otherwise the transcript control is opened first.
  autoActivate('page-load');

  return {
    dispose(): void {
      if (disposed) return;
      disposed = true;
      pageDocument.removeEventListener('click', onTranscriptClick, true);
      pageWindow.removeEventListener('popstate', onPopState);
      if (urlPollId !== null) pageWindow.clearInterval(urlPollId);
      urlPollId = null;
      if (run) disconnectRun(run);
      run = null;
    },
    getStatus(): PageCaptureStatus {
      return status;
    },
    getLastUrl(): string {
      return lastUrl;
    },
  };
}

let contentRuntimeInstalled = false;

/**
 * Install the production capture pipeline once per page context. The Stage 0
 * selectors are embedded by the build step; under Vitest the placeholder is
 * undefined and this returns null without touching the page.
 */
export function installContentRuntime(): ContentScriptController | null {
  if (contentRuntimeInstalled) return null;
  if (typeof document === 'undefined' || typeof chrome === 'undefined') {
    return null;
  }
  if (typeof __STAGE0_SELECTORS__ === 'undefined') return null;

  contentRuntimeInstalled = true;
  console.log("[lecture-transcripts] content runtime build=overview-iframe-1");
  return createContentScript(
    createProductionContentDependencies(__STAGE0_SELECTORS__),
  );
}

installContentRuntime();
