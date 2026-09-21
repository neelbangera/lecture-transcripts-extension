/**
 * Parser for the verified Stage 0 Leccap player shape.
 *
 * The parser deliberately receives selector and course configuration instead
 * of reading files at runtime. The build step can embed those verified
 * values, while tests can load the committed Stage 0 fixtures. No selector
 * below is inferred from arbitrary page text: the three transcript row
 * selectors are the exact rendered DOM selectors recorded in
 * docs/STAGE_0_LECCAP_OBSERVATIONS.md.
 */

export type ParserRejectionStatus =
  | "not_ready"
  | "rejected_missing_identity"
  | "rejected_ambiguous_metadata"
  | "rejected_unsafe_url";

export type CompletionResult = "complete" | "not_ready";

export interface SelectorCapture {
  selector: string;
  regex: string;
  captureGroup: number;
}

export interface TermSelectorCapture {
  selector: string;
  regex: string;
  seasonCaptureGroup: number;
  yearCaptureGroup: number;
}

export interface LectureDateSource {
  page: "lecture_page" | "linked_overview_page";
  runtimeLookup:
    | "on_demand_fetch"
    | "landing_cache"
    | "unsupported_without_date";
  lecturePageOverviewLinkSelector: string | null;
  recordingCardSelector: string | null;
  recordingLinkSelector: string | null;
  dateSelector: string;
  dateRegex: string;
  dateCaptureGroup: number;
  correlation:
    | "absolute_player_href"
    | "normalized_player_path"
    | "not_applicable";
}

export interface CompletionIndicator {
  selector: string;
  mode: "present" | "attribute_equals" | "text_matches";
  attribute: string | null;
  valueRegex: string | null;
  populationSelector: string | null;
}

export interface SelectorFixture {
  transcriptButtonSelector: string;
  transcriptContainerSelector: string;
  courseSelector: SelectorCapture;
  termSelector: TermSelectorCapture;
  lectureNumberSelector: SelectorCapture;
  lectureDateSource: LectureDateSource;
  dateYearPolicy: "term_year_if_missing";
  completionIndicator: CompletionIndicator;
  loadingIndicatorSelector: string | null;
  timestampFormat: string;
  isSPA: boolean;
  stabilityDebounceMs: number;
  sanityMinChars: number;
  loadingTextMarkers: readonly string[];
}

export interface CourseMapping {
  pageCourseText: string;
  courseName: string;
  courseSlug: string;
  supportedTerms: readonly string[];
}

export interface ParserOptions {
  selectors: SelectorFixture;
  courseMappings: readonly CourseMapping[];
  /**
   * The current player URL. If omitted, document.location.href is used.
   * Tests pass this explicitly so the fixture never depends on the test
   * runner's own URL.
   */
  sourceUrl?: string;
  /**
   * Runtime overview fetch. In production this is the same-origin browser
   * fetch. Tests provide a fixture-backed function and can inspect its URL.
   */
  fetchOverview?: OverviewFetcher;
  /**
   * The content observer calls the parser only after two equal stable
   * snapshots. The default is therefore the contract's required value of 2;
   * the option lets the observer pass the measured count through for tests.
   */
  stableSnapshotCount?: number;
}

export interface OverviewResponse {
  ok: boolean;
  text(): Promise<string>;
}

export type OverviewFetcher = (
  input: string,
  init?: RequestInit,
) => Promise<OverviewResponse>;

export interface ParsedLecture {
  supported: true;
  completion: "complete";
  courseName: string;
  courseSlug: string;
  term: string;
  lectureNumber: number;
  lectureDate: string;
  sourceUrl: string;
  transcript: string;
  timestampedTranscript: string;
  derivedFrom: "both" | "timestamped-only" | "plain-only";
  lectureKey: string;
  contentHash: string;
  stableSnapshotCount: number;
}

export interface RejectedLecture {
  supported: false;
  completion: CompletionResult;
  status: ParserRejectionStatus;
  reason: string;
  courseName?: string;
  term?: string;
  lectureNumber?: number | null;
}

export type LectureParseResult = ParsedLecture | RejectedLecture;

/** The exact timestamp grammar from TECHNICAL_PLAN.md. */
export const NORMATIVE_TIMESTAMP_PREFIX =
  /^\s*[\[(]?\d{1,2}:\d{2}(?::\d{2})?[\]\)]?\s*(?:[-–—|]\s*)?/;

/**
 * The rendered Leccap row selectors are not runtime-discovered. They are the
 * observed selectors documented in the Stage 0 evidence packet.
 */
const TRANSCRIPT_ROW_SELECTOR = ".transcript-row";
const TRANSCRIPT_TIME_SELECTOR = ".transcript-time";
const TRANSCRIPT_TEXT_SELECTOR = ".transcript-text";
const SOURCE_URL_HOST = "leccap.engin.umich.edu";

const LOADING_ONLY_TRANSCRIPT = /^(?:loading…|loading transcript|no transcript)$/i;
const RAW_TIMESTAMP = /^\d{1,2}:\d{2}(?::\d{2})?$/;
const MONTH_NAMES: Record<string, number> = {
  january: 1,
  february: 2,
  march: 3,
  april: 4,
  may: 5,
  june: 6,
  july: 7,
  august: 8,
  september: 9,
  october: 10,
  november: 11,
  december: 12,
};

function reject(
  status: ParserRejectionStatus,
  reason: string,
  context: Partial<RejectedLecture> = {},
): RejectedLecture {
  return {
    supported: false,
    completion: status === "not_ready" ? "not_ready" : "complete",
    status,
    reason,
    ...context,
  };
}

function textOf(element: Element | null): string {
  return element?.textContent ?? "";
}

function normalizedMetadataText(value: string): string {
  return value.normalize("NFC").replace(/[\t\r\n ]+/g, " ").trim();
}

function queryOne(root: ParentNode, selector: string): Element | null {
  try {
    return root.querySelector(selector);
  } catch {
    return null;
  }
}

function queryAll(root: ParentNode, selector: string): Element[] {
  try {
    return Array.from(root.querySelectorAll(selector));
  } catch {
    return [];
  }
}

function compileRegex(source: string, flags = ""): RegExp | null {
  try {
    return new RegExp(source, flags);
  } catch {
    return null;
  }
}

function capture(match: RegExpExecArray | null, group: number): string | null {
  if (!match || !Number.isInteger(group) || group < 1) {
    return null;
  }
  const value = match[group];
  return typeof value === "string" && value.trim() !== "" ? value.trim() : null;
}

function sameOriginLeccapUrl(rawUrl: string, baseUrl?: string): URL | null {
  try {
    const url = new URL(rawUrl, baseUrl);
    if (
      url.protocol !== "https:" ||
      url.hostname.toLowerCase() !== SOURCE_URL_HOST ||
      url.username !== "" ||
      url.password !== "" ||
      (url.port !== "" && url.port !== "443")
    ) {
      return null;
    }
    return url;
  } catch {
    return null;
  }
}

/**
 * Canonicalize a Leccap URL for identity and exact player-link correlation.
 * Query strings and fragments are deliberately discarded; encoded path bytes
 * and trailing slashes remain intact.
 */
export function canonicalizeLeccapUrl(
  rawUrl: string,
  baseUrl?: string,
): string | null {
  const url = sameOriginLeccapUrl(rawUrl, baseUrl);
  if (!url) {
    return null;
  }
  url.search = "";
  url.hash = "";
  url.port = "";
  if (url.pathname === "") {
    url.pathname = "/";
  }
  return `https://${SOURCE_URL_HOST}${url.pathname}`;
}

function getDocumentUrl(document: Document, sourceUrl?: string): string | null {
  if (sourceUrl) {
    return sourceUrl;
  }
  const documentLocation = document.defaultView?.location?.href;
  if (documentLocation) {
    return documentLocation;
  }
  return null;
}

function selectorMatch(
  document: Document,
  selector: string,
  mode: CompletionIndicator["mode"],
  attribute: string | null,
  valueRegex: string | null,
): boolean {
  const elements = queryAll(document, selector);
  if (elements.length === 0) {
    return false;
  }
  if (mode === "present") {
    return true;
  }
  const compiled = valueRegex ? compileRegex(valueRegex) : null;
  if (!compiled) {
    return false;
  }
  return elements.some((element) => {
    const candidate =
      mode === "attribute_equals"
        ? attribute
          ? element.getAttribute(attribute)
          : null
        : normalizedMetadataText(textOf(element));
    return candidate !== null && compiled.test(candidate);
  });
}

function loadingRegionIsClear(
  container: Element,
  selectors: SelectorFixture,
): boolean {
  const selector = selectors.loadingIndicatorSelector;
  if (selector === null) {
    return true;
  }
  const regions = queryAll(container, selector);
  const markers = selectors.loadingTextMarkers
    .map((marker) => normalizedMetadataText(marker).toLowerCase())
    .filter(Boolean);
  if (markers.length === 0) {
    return true;
  }
  return !regions.some((region) => {
    const regionText = normalizedMetadataText(textOf(region)).toLowerCase();
    return markers.some((marker) => regionText.includes(marker));
  });
}

function completionIsReady(
  document: Document,
  container: Element,
  selectors: SelectorFixture,
): boolean {
  const indicator = selectors.completionIndicator;
  if (
    !selectorMatch(
      document,
      indicator.selector,
      indicator.mode,
      indicator.attribute,
      indicator.valueRegex,
    )
  ) {
    return false;
  }
  if (!loadingRegionIsClear(container, selectors)) {
    return false;
  }
  if (indicator.populationSelector === null) {
    return true;
  }
  const population = queryAll(container, indicator.populationSelector);
  return population.some((element) => textOf(element).trim() !== "");
}

function normalizeTranscript(input: string): string {
  const normalized = input.normalize("NFC").replace(/\r\n?/g, "\n");
  const lines = normalized.split("\n").map((line) => {
    const timestampMatch = line.match(NORMATIVE_TIMESTAMP_PREFIX);
    const remainder = timestampMatch
      ? line.slice(timestampMatch[0].length)
      : line;
    const body = remainder.replace(/[ \t]+$/g, "").replace(/[ \t]{2,}/g, " ");
    if (!timestampMatch) {
      return body;
    }
    const prefix = timestampMatch[0].replace(/[ \t]+$/g, "");
    return body === "" ? prefix : `${prefix} ${body}`;
  });
  return lines
    .join("\n")
    .replace(/\n{3,}/g, "\n\n")
    .replace(/^\n+|\n+$/g, "");
}

function concatBytes(...parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((sum, part) => sum + part.byteLength, 0);
  const output = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    output.set(part, offset);
    offset += part.byteLength;
  }
  return output;
}

function bytesAsHex(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

/** Compute the normative framed transcript hash without Node-only APIs. */
export async function hashTranscriptForms(
  transcript: string,
  timestampedTranscript: string,
): Promise<string> {
  const encoder = new TextEncoder();
  const plainBytes = encoder.encode(transcript);
  const timestampedBytes = encoder.encode(timestampedTranscript);
  const framed = concatBytes(
    encoder.encode("transcript-hash-v1\0"),
    encoder.encode(String(plainBytes.byteLength)),
    encoder.encode(":"),
    plainBytes,
    encoder.encode(String(timestampedBytes.byteLength)),
    encoder.encode(":"),
    timestampedBytes,
  );
  const digest = await crypto.subtle.digest("SHA-256", framed);
  return bytesAsHex(new Uint8Array(digest));
}

interface ExtractedTranscript {
  transcript: string;
  timestampedTranscript: string;
  derivedFrom: "both" | "timestamped-only" | "plain-only";
}

function extractTranscript(
  container: Element,
  selectors: SelectorFixture,
): ExtractedTranscript | RejectedLecture {
  const rows = queryAll(container, TRANSCRIPT_ROW_SELECTOR);
  if (rows.length === 0) {
    return reject("not_ready", "the transcript has no rendered rows");
  }

  const populatedRows = rows
    .map((row) => {
      const text = textOf(queryOne(row, TRANSCRIPT_TEXT_SELECTOR));
      const time = textOf(queryOne(row, TRANSCRIPT_TIME_SELECTOR)).trim();
      return { text, time };
    })
    .filter(({ text }) => text.trim() !== "");

  if (populatedRows.length === 0) {
    return reject("not_ready", "the transcript rows are empty");
  }

  const hasTime = populatedRows.map(({ time }) => time !== "");
  const hasTimestampedRows = hasTime.some(Boolean);
  const hasPlainOnlyRows = hasTime.some((value) => !value);
  if (hasTimestampedRows && hasPlainOnlyRows) {
    return reject(
      "rejected_ambiguous_metadata",
      "transcript rows mix timestamped and plain-only shapes",
    );
  }

  if (hasTimestampedRows && populatedRows.some(({ time }) => !RAW_TIMESTAMP.test(time))) {
    return reject(
      "rejected_ambiguous_metadata",
      "a transcript timestamp does not match the observed MM:SS or H:MM:SS shape",
    );
  }

  const plain = normalizeTranscript(populatedRows.map(({ text }) => text).join("\n"));
  if (LOADING_ONLY_TRANSCRIPT.test(plain)) {
    return reject("not_ready", "the transcript contains only a loading marker");
  }
  const minChars = selectors.sanityMinChars;
  if ((plain.match(/\S/g) ?? []).length < minChars) {
    return reject(
      "not_ready",
      `the normalized transcript has fewer than ${minChars} non-whitespace characters`,
    );
  }

  if (!hasTimestampedRows) {
    return {
      transcript: plain,
      timestampedTranscript: "",
      derivedFrom: "plain-only",
    };
  }

  const timestamped = normalizeTranscript(
    populatedRows
      .map(({ time, text }) => `[${time}] ${text}`)
      .join("\n"),
  );
  return {
    transcript: plain,
    timestampedTranscript: timestamped,
    derivedFrom: "both",
  };
}

function parseDateParts(month: number, day: number, year: number): string | null {
  if (
    !Number.isInteger(month) ||
    !Number.isInteger(day) ||
    !Number.isInteger(year) ||
    year < 1 ||
    year > 9999 ||
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > 31
  ) {
    return null;
  }
  const date = new Date(Date.UTC(year, month - 1, day));
  if (
    date.getUTCFullYear() !== year ||
    date.getUTCMonth() !== month - 1 ||
    date.getUTCDate() !== day
  ) {
    return null;
  }
  return `${String(year).padStart(4, "0")}-${String(month).padStart(2, "0")}-${String(day).padStart(2, "0")}`;
}

/**
 * Parse the Stage 0 date capture. Numeric M/D/YYYY is month-first. A
 * yearless textual month/day is allowed only with the already-validated term
 * year; yearless numeric dates are rejected as locale-ambiguous.
 */
export function parseRecordingDate(
  dateText: string,
  dateRegex: string,
  dateCaptureGroup: number,
  term: string,
): string | null {
  const matcher = compileRegex(dateRegex);
  if (!matcher) {
    return null;
  }
  const match = matcher.exec(dateText);
  const captured = capture(match, dateCaptureGroup);
  if (!captured) {
    return null;
  }

  const numeric = captured.match(/^(\d{1,2})\/(\d{1,2})\/(\d{4})$/);
  if (numeric) {
    return parseDateParts(
      Number(numeric[1]),
      Number(numeric[2]),
      Number(numeric[3]),
    );
  }
  if (/^\d{1,2}\/\d{1,2}$/.test(captured)) {
    return null;
  }

  const textual = captured.match(
    /^(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{1,2})(?:,?\s+(\d{4}))?$/i,
  );
  if (!textual) {
    return null;
  }
  const yearMatch = term.match(/^(\d{4})-(?:winter|spring|summer|fall)$/);
  if (!yearMatch) {
    return null;
  }
  const month = MONTH_NAMES[textual[1].toLowerCase()];
  const day = Number(textual[2]);
  const year = textual[3] ? Number(textual[3]) : Number(yearMatch[1]);
  return parseDateParts(month, day, year);
}

async function resolveLectureDate(
  document: Document,
  sourceUrl: string,
  term: string,
  selectors: SelectorFixture,
  fetchOverview: OverviewFetcher | undefined,
): Promise<string | RejectedLecture> {
  const source = selectors.lectureDateSource;
  if (source.page === "lecture_page") {
    const dateElement = queryOne(document, source.dateSelector);
    if (!dateElement) {
      return reject(
        "rejected_ambiguous_metadata",
        "the lecture page has no configured recording-date element",
      );
    }
    const parsed = parseRecordingDate(
      textOf(dateElement),
      source.dateRegex,
      source.dateCaptureGroup,
      term,
    );
    return (
      parsed ??
      reject(
        "rejected_ambiguous_metadata",
        "the configured lecture-page date is missing or invalid",
      )
    );
  }

  if (
    source.runtimeLookup !== "on_demand_fetch" ||
    source.lecturePageOverviewLinkSelector === null ||
    source.recordingCardSelector === null ||
    source.recordingLinkSelector === null
  ) {
    return reject(
      "rejected_ambiguous_metadata",
      "the selected linked-overview date policy is not executable",
    );
  }

  const overviewLink = queryOne(document, source.lecturePageOverviewLinkSelector);
  const overviewHref = overviewLink?.getAttribute("href");
  if (!overviewHref) {
    return reject(
      "rejected_ambiguous_metadata",
      "the lecture page has no linked overview href",
    );
  }
  const overviewUrl = canonicalizeLeccapUrl(overviewHref, sourceUrl);
  const currentPlayerUrl = canonicalizeLeccapUrl(sourceUrl);
  if (!overviewUrl || !currentPlayerUrl) {
    return reject(
      "rejected_ambiguous_metadata",
      "the linked overview or current player URL is not a safe Leccap URL",
    );
  }
  const fetcher =
    fetchOverview ??
    ((globalThis as typeof globalThis & { fetch?: OverviewFetcher }).fetch as
      | OverviewFetcher
      | undefined);
  if (!fetcher) {
    return reject(
      "rejected_ambiguous_metadata",
      "no same-origin overview fetch is available",
    );
  }

  let response: OverviewResponse;
  try {
    response = await fetcher(overviewUrl, { credentials: "same-origin" });
  } catch {
    return reject(
      "rejected_ambiguous_metadata",
      "the linked overview fetch failed",
    );
  }
  if (!response || !response.ok) {
    return reject(
      "rejected_ambiguous_metadata",
      "the linked overview fetch was not successful",
    );
  }

  let overviewHtml: string;
  try {
    overviewHtml = await response.text();
  } catch {
    return reject(
      "rejected_ambiguous_metadata",
      "the linked overview response could not be read",
    );
  }

  const parserConstructor =
    document.defaultView?.DOMParser ??
    (globalThis as typeof globalThis & { DOMParser?: typeof DOMParser }).DOMParser;
  if (!parserConstructor) {
    return reject(
      "rejected_ambiguous_metadata",
      "the runtime has no DOM parser for the linked overview",
    );
  }
  const overviewDocument = new parserConstructor().parseFromString(
    overviewHtml,
    "text/html",
  );
  if (!overviewDocument) {
    return reject(
      "rejected_ambiguous_metadata",
      "the linked overview could not be parsed",
    );
  }

  const cards = queryAll(overviewDocument, source.recordingCardSelector);
  const matches = cards.filter((card) => {
    const link = queryOne(card, source.recordingLinkSelector!);
    const href = link?.getAttribute("href");
    return href !== null && canonicalizeLeccapUrl(href, overviewUrl) === currentPlayerUrl;
  });
  if (matches.length !== 1) {
    return reject(
      "rejected_ambiguous_metadata",
      `the linked overview has ${matches.length} player-link matches; exactly one is required`,
    );
  }
  const dateElement = queryOne(matches[0], source.dateSelector);
  if (!dateElement) {
    return reject(
      "rejected_ambiguous_metadata",
      "the correlated overview recording has no date element",
    );
  }
  const parsed = parseRecordingDate(
    textOf(dateElement),
    source.dateRegex,
    source.dateCaptureGroup,
    term,
  );
  return (
    parsed ??
    reject(
      "rejected_ambiguous_metadata",
      "the correlated overview recording date is missing or invalid",
    )
  );
}

function extractIdentity(
  document: Document,
  selectors: SelectorFixture,
  mappings: readonly CourseMapping[],
):
  | {
      courseName: string;
      courseSlug: string;
      term: string;
      lectureNumber: number;
    }
  | RejectedLecture {
  const courseElement = queryOne(document, selectors.courseSelector.selector);
  const termElement = queryOne(document, selectors.termSelector.selector);
  const numberElement = queryOne(document, selectors.lectureNumberSelector.selector);
  if (!courseElement || !termElement) {
    return reject(
      "rejected_missing_identity",
      "the known lecture page is missing its course/term header",
    );
  }

  const courseText = normalizedMetadataText(textOf(courseElement));
  const courseMatcher = compileRegex(selectors.courseSelector.regex, "i");
  const termMatcher = compileRegex(selectors.termSelector.regex, "i");
  const courseMatch = courseMatcher?.exec(courseText) ?? null;
  const termMatch = termMatcher?.exec(normalizedMetadataText(textOf(termElement))) ?? null;
  const pageCourseText = capture(courseMatch, selectors.courseSelector.captureGroup);
  const season = capture(termMatch, selectors.termSelector.seasonCaptureGroup);
  const year = capture(termMatch, selectors.termSelector.yearCaptureGroup);
  if (!pageCourseText || !season || !year || !/^\d{4}$/.test(year)) {
    return reject(
      "rejected_missing_identity",
      "the course/term header does not match the verified Stage 0 shape",
    );
  }
  const normalizedSeason = season.toLowerCase();
  if (!/^(?:winter|spring|summer|fall)$/.test(normalizedSeason)) {
    return reject(
      "rejected_ambiguous_metadata",
      "the page reports an unsupported or ambiguous term season",
    );
  }
  const term = `${year}-${normalizedSeason}`;
  const normalizedPageCourseText = normalizedMetadataText(pageCourseText);
  const matchingMappings = mappings.filter(
    (mapping) =>
      normalizedMetadataText(mapping.pageCourseText) === normalizedPageCourseText &&
      mapping.supportedTerms.filter((supportedTerm) => supportedTerm === term).length === 1,
  );
  if (matchingMappings.length !== 1) {
    return reject(
      "rejected_ambiguous_metadata",
      "the page course/term pair is not exactly one verified supported mapping",
      { courseName: normalizedPageCourseText, term },
    );
  }
  const [mapping] = matchingMappings;
  if (!/^[a-z0-9]+$/.test(mapping.courseSlug) || mapping.courseName.includes("\n")) {
    return reject(
      "rejected_ambiguous_metadata",
      "the verified course mapping is not path-safe",
      { courseName: mapping.courseName, term },
    );
  }

  if (!numberElement) {
    return reject(
      "rejected_ambiguous_metadata",
      "the recording title has no numeric lecture prefix; the overview badge is not an identity",
      { courseName: mapping.courseName, term, lectureNumber: null },
    );
  }
  const numberMatcher = compileRegex(selectors.lectureNumberSelector.regex);
  const numberMatch = numberMatcher?.exec(textOf(numberElement)) ?? null;
  const numberText = capture(numberMatch, selectors.lectureNumberSelector.captureGroup);
  const lectureNumber = numberText ? Number(numberText) : Number.NaN;
  if (!numberText || !Number.isInteger(lectureNumber) || lectureNumber < 1 || lectureNumber > 999) {
    return reject(
      "rejected_ambiguous_metadata",
      "the recording title has no valid numeric lecture prefix; the overview badge is not an identity",
      { courseName: mapping.courseName, term, lectureNumber: null },
    );
  }
  return {
    courseName: mapping.courseName,
    courseSlug: mapping.courseSlug,
    term,
    lectureNumber,
  };
}

/** Parse one already-stable, already-activated rendered lecture page. */
export async function parseLecturePage(
  document: Document,
  options: ParserOptions,
): Promise<LectureParseResult> {
  const rawSourceUrl = getDocumentUrl(document, options.sourceUrl);
  if (!rawSourceUrl) {
    return reject("rejected_unsafe_url", "the lecture page has no source URL");
  }
  const sourceUrl = canonicalizeLeccapUrl(rawSourceUrl);
  if (!sourceUrl) {
    return reject(
      "rejected_unsafe_url",
      "the source URL is not an HTTPS Leccap player URL",
    );
  }

  const selectors = options.selectors;
  const container = queryOne(document, selectors.transcriptContainerSelector);
  if (!container) {
    const hasKnownHeader =
      queryOne(document, selectors.courseSelector.selector) !== null ||
      queryOne(document, selectors.lectureNumberSelector.selector) !== null;
    return reject(
      hasKnownHeader ? "not_ready" : "rejected_missing_identity",
      hasKnownHeader
        ? "the verified transcript container is not present"
        : "the page does not have the verified Leccap lecture-player shape",
    );
  }
  if (!completionIsReady(document, container, selectors)) {
    return reject(
      "not_ready",
      "the transcript is not open, populated, or clear of its configured loading region",
    );
  }
  const stableSnapshotCount = options.stableSnapshotCount ?? 2;
  if (!Number.isInteger(stableSnapshotCount) || stableSnapshotCount < 2) {
    return reject(
      "not_ready",
      "the parser requires two identical stable snapshots before submission",
    );
  }

  const identity = extractIdentity(document, selectors, options.courseMappings);
  if ("supported" in identity && identity.supported === false) {
    return identity;
  }
  const transcript = extractTranscript(container, selectors);
  if ("supported" in transcript && transcript.supported === false) {
    return transcript;
  }
  const lectureDate = await resolveLectureDate(
    document,
    sourceUrl,
    identity.term,
    selectors,
    options.fetchOverview,
  );
  if (typeof lectureDate !== "string") {
    return {
      ...lectureDate,
      courseName: identity.courseName,
      term: identity.term,
      lectureNumber: identity.lectureNumber,
    };
  }

  const contentHash = await hashTranscriptForms(
    transcript.transcript,
    transcript.timestampedTranscript,
  );
  return {
    supported: true,
    completion: "complete",
    courseName: identity.courseName,
    courseSlug: identity.courseSlug,
    term: identity.term,
    lectureNumber: identity.lectureNumber,
    lectureDate,
    sourceUrl,
    transcript: transcript.transcript,
    timestampedTranscript: transcript.timestampedTranscript,
    derivedFrom: transcript.derivedFrom,
    lectureKey: `${identity.courseSlug}/${identity.term}/${String(identity.lectureNumber).padStart(3, "0")}`,
    contentHash,
    stableSnapshotCount,
  };
}
