import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";

import type { CourseMapping, SelectorFixture } from "../extension/src/leccap-parser";

const fixtureDirectory = fileURLToPath(new URL("./fixtures/", import.meta.url));

function readFixture(name: string): string {
  return readFileSync(`${fixtureDirectory}${name}`, "utf8");
}

function readJson<T>(name: string): T {
  return JSON.parse(readFixture(name)) as T;
}

function expectNonemptyString(value: unknown, label: string): string {
  expect(typeof value, `${label} must be a string`).toBe("string");
  const text = value as string;
  expect(text.length, `${label} must be non-empty`).toBeGreaterThan(0);
  return text;
}

function normalizePageLabel(value: string): string {
  return value.normalize("NFC").replace(/[\t\r\n ]+/g, " ").trim();
}

function collectStringValues(
  value: unknown,
  path: string,
  results: Array<{ path: string; text: string }>,
): void {
  if (typeof value === "string") {
    results.push({ path, text: value });
    return;
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) =>
      collectStringValues(item, `${path}[${index}]`, results),
    );
    return;
  }
  if (typeof value === "object" && value !== null) {
    for (const [key, item] of Object.entries(value)) {
      collectStringValues(item, `${path}.${key}`, results);
    }
  }
}

interface SizeSample {
  sampleId: string;
  courseSlug: string;
  term: string;
  lectureNumber: number;
  transcriptBytes: number;
  timestampedTranscriptBytes: number;
  serializedJobBytes: number;
  nativeMessageBytes: number;
  statusPageBytes: number;
  renderTimeMs: number | null;
  [key: string]: unknown;
}

interface SizeReport {
  capturedAt: string;
  samples: SizeSample[];
  observedMaxima: Record<string, number | null>;
  approvedLimits: Record<string, number>;
  renderTimeMeasurement?: { status?: string };
}

function maximaFromSamples(samples: readonly SizeSample[], key: string): number | null {
  const values: number[] = [];
  for (const sample of samples) {
    const value = sample[key];
    if (typeof value === "number") {
      values.push(value);
    }
  }
  return values.length === 0 ? null : Math.max(...values);
}

const LECTURE_KEY_PATTERN =
  /^[a-z0-9]+\/[0-9]{4}-(winter|spring|summer|fall)\/[0-9]{3}$/;
const CONTENT_HASH_PATTERN = /^[0-9a-f]{64}$/;
const COURSE_SLUG_PATTERN = /^[a-z0-9]+$/;
const TERM_PATTERN = /^\d{4}-(winter|spring|summer|fall)$/;
const DERIVED_FROM_VALUES = ["both", "timestamped-only", "plain-only"];
const PLACEHOLDER_PATTERN = /TBD|e\.g\.|CSS selector string/i;
const MEDIA_URL_PATTERN =
  /\.(?:mp4|m4v|mov|webm|m3u8|mpd|vtt|webvtt|srt|mp3|m4a|aac|wav)(?:[?#]|$)/i;
const ABSOLUTE_URL_PATTERN = /https?:\/\/[^\s"'<>)]+/gi;
const ATTRIBUTE_URL_PATTERN = /\b(?:href|src)\s*=\s*"([^"]*)"/gi;
const ROUTE_ID_PATTERN = /(?:player\/r|\bsite)\/([A-Za-z0-9_-]+)/gi;
const BARE_ROUTE_ID_PATTERN = /\br\/([A-Za-z0-9_-]+)/gi;
const SANITIZED_ROUTE_ID_PATTERN = /^sanitized[a-z0-9]+$/i;
const SECRET_PATTERNS: Array<[string, RegExp]> = [
  ["set-cookie", /set-cookie/i],
  ["sessionid", /sessionid/i],
  ["token=", /\btoken\s*=/i],
  ["cookie=", /\bcookie\s*=/i],
  ["authorization header", /authorization\s*:/i],
  ["bearer token", /bearer\s+\S+/i],
  ["access_token", /access_token/i],
  ["refresh_token", /refresh_token/i],
  ["device_code", /device_code/i],
  ["user_code", /user_code/i],
  ["email identifier", /[\w.+-]+@[\w-]+\.[a-z]{2,}/i],
];
const HTML_FIXTURES = [
  "lecture-page.html",
  "no-number-lecture-page.html",
  "overview-page.html",
  "loading-transcript-page.html",
  "non-lecture-page.html",
];
const EXPECTED_FIXTURES = [
  "lecture-page.expected.json",
  "no-number-lecture-page.expected.json",
];

const selectors = readJson<SelectorFixture>("lecture-page.selectors.json");
const selectorsRecord = selectors as unknown as Record<string, unknown>;
const courseMapping = readJson<{ courseMappings: CourseMapping[] }>(
  "course-mapping.json",
);
const supportedPairs = new Set(
  courseMapping.courseMappings.flatMap((entry) =>
    entry.supportedTerms.map((supportedTerm) => `${entry.courseSlug}/${supportedTerm}`),
  ),
);
const sizeReport = readJson<SizeReport>("transcript-size-report.json");
const lectureDocument = new JSDOM(readFixture("lecture-page.html")).window.document;
const overviewDocument = new JSDOM(readFixture("overview-page.html")).window.document;

describe("Stage 0 fixture packet contract", () => {
  describe("lecture-page.selectors.json", () => {
    it("contains every required selector field with a nonempty value", () => {
      expectNonemptyString(selectors.transcriptButtonSelector, "transcriptButtonSelector");
      expectNonemptyString(
        selectors.transcriptContainerSelector,
        "transcriptContainerSelector",
      );
      expectNonemptyString(selectors.timestampFormat, "timestampFormat");

      for (const key of ["courseSelector", "lectureNumberSelector"] as const) {
        expectNonemptyString(selectors[key].selector, `${key}.selector`);
        expectNonemptyString(selectors[key].regex, `${key}.regex`);
        expect(Number.isInteger(selectors[key].captureGroup)).toBe(true);
        expect(selectors[key].captureGroup).toBeGreaterThanOrEqual(1);
      }

      expectNonemptyString(selectors.termSelector.selector, "termSelector.selector");
      expectNonemptyString(selectors.termSelector.regex, "termSelector.regex");
      expect(Number.isInteger(selectors.termSelector.seasonCaptureGroup)).toBe(true);
      expect(Number.isInteger(selectors.termSelector.yearCaptureGroup)).toBe(true);
      expect(selectors.termSelector.seasonCaptureGroup).toBeGreaterThanOrEqual(1);
      expect(selectors.termSelector.yearCaptureGroup).toBeGreaterThanOrEqual(1);
      expect(selectors.termSelector.seasonCaptureGroup).not.toBe(
        selectors.termSelector.yearCaptureGroup,
      );

      expect(selectors.dateYearPolicy).toBe("term_year_if_missing");
      expect(typeof selectors.isSPA).toBe("boolean");
      expect(Array.isArray(selectors.loadingTextMarkers)).toBe(true);
      for (const [index, marker] of selectors.loadingTextMarkers.entries()) {
        expectNonemptyString(marker, `loadingTextMarkers[${index}]`);
      }

      expect(
        selectors.loadingIndicatorSelector === null ||
          typeof selectors.loadingIndicatorSelector === "string",
      ).toBe(true);
      if (selectors.loadingIndicatorSelector === null) {
        expectNonemptyString(
          selectorsRecord.loadingRegionNote,
          "loadingRegionNote for the explicit null loadingIndicatorSelector",
        );
      }

      for (const [label, source] of [
        ["courseSelector.regex", selectors.courseSelector.regex],
        ["termSelector.regex", selectors.termSelector.regex],
        ["lectureNumberSelector.regex", selectors.lectureNumberSelector.regex],
      ] as const) {
        expect(() => new RegExp(source), `${label} must compile`).not.toThrow();
      }
    });

    it("uses a valid completion mode with mode-consistent fields and live selectors", () => {
      const completion = selectors.completionIndicator;
      expectNonemptyString(completion.selector, "completionIndicator.selector");
      expect(["present", "attribute_equals", "text_matches"]).toContain(completion.mode);

      if (completion.mode === "present") {
        expect(completion.attribute).toBeNull();
        expect(completion.valueRegex).toBeNull();
      } else if (completion.mode === "attribute_equals") {
        expectNonemptyString(completion.attribute, "completionIndicator.attribute");
        expectNonemptyString(completion.valueRegex, "completionIndicator.valueRegex");
      } else {
        expect(completion.attribute).toBeNull();
        expectNonemptyString(completion.valueRegex, "completionIndicator.valueRegex");
      }

      expectNonemptyString(
        completion.populationSelector,
        "completionIndicator.populationSelector",
      );

      const valueRegex =
        completion.mode === "present"
          ? null
          : new RegExp(completion.valueRegex as string);
      const indicatorElements = Array.from(
        lectureDocument.querySelectorAll(completion.selector),
      );
      expect(indicatorElements.length).toBeGreaterThan(0);
      expect(
        indicatorElements.some((element) => {
          if (completion.mode === "present") {
            return true;
          }
          if (completion.mode === "attribute_equals") {
            const attributeValue = element.getAttribute(
              completion.attribute as string,
            );
            return attributeValue !== null && (valueRegex as RegExp).test(attributeValue);
          }
          return (valueRegex as RegExp).test((element.textContent ?? "").trim());
        }),
      ).toBe(true);

      const populationElements = Array.from(
        lectureDocument.querySelectorAll(completion.populationSelector as string),
      );
      expect(populationElements.length).toBeGreaterThan(0);
      expect(
        populationElements.some((element) => (element.textContent ?? "").trim().length > 0),
      ).toBe(true);
    });

    it("records an executable linked-overview on-demand date policy", () => {
      const dateSource = selectors.lectureDateSource;
      expect(["lecture_page", "linked_overview_page"]).toContain(dateSource.page);
      expect(dateSource.page).toBe("linked_overview_page");
      expect([
        "on_demand_fetch",
        "landing_cache",
        "unsupported_without_date",
      ]).toContain(dateSource.runtimeLookup);
      expect(dateSource.runtimeLookup).toBe("on_demand_fetch");
      expectNonemptyString(
        dateSource.lecturePageOverviewLinkSelector,
        "lectureDateSource.lecturePageOverviewLinkSelector",
      );
      expectNonemptyString(
        dateSource.recordingCardSelector,
        "lectureDateSource.recordingCardSelector",
      );
      expectNonemptyString(
        dateSource.recordingLinkSelector,
        "lectureDateSource.recordingLinkSelector",
      );
      expectNonemptyString(dateSource.dateSelector, "lectureDateSource.dateSelector");
      expectNonemptyString(dateSource.dateRegex, "lectureDateSource.dateRegex");
      expect(Number.isInteger(dateSource.dateCaptureGroup)).toBe(true);
      expect(dateSource.dateCaptureGroup).toBeGreaterThanOrEqual(1);
      expect([
        "absolute_player_href",
        "normalized_player_path",
        "not_applicable",
      ]).toContain(dateSource.correlation);
      expect(dateSource.correlation).toBe("absolute_player_href");
      const dateSourceRecord = dateSource as unknown as Record<string, unknown>;
      expect(dateSourceRecord.dateOrder).toBe("month-first");

      const overviewLink = lectureDocument.querySelector(
        dateSource.lecturePageOverviewLinkSelector as string,
      );
      expect(overviewLink).not.toBeNull();
      expectNonemptyString(
        overviewLink?.getAttribute("href"),
        "lecture page overview link href",
      );

      const recordingCards = Array.from(
        overviewDocument.querySelectorAll(dateSource.recordingCardSelector as string),
      );
      expect(recordingCards.length).toBeGreaterThan(0);
      const recordingLinks = Array.from(
        overviewDocument.querySelectorAll(dateSource.recordingLinkSelector as string),
      );
      expect(recordingLinks.length).toBeGreaterThan(0);

      const dateRegex = new RegExp(dateSource.dateRegex);
      const dateElements = Array.from(
        overviewDocument.querySelectorAll(dateSource.dateSelector),
      );
      expect(dateElements.length).toBeGreaterThan(0);
      for (const element of dateElements) {
        const match = dateRegex.exec(element.textContent ?? "");
        expect(
          match,
          `rec-date "${element.textContent}" must match dateRegex`,
        ).not.toBeNull();
        const captured = match?.[dateSource.dateCaptureGroup];
        expectNonemptyString(captured, "dateCaptureGroup value");
        expect(captured).toMatch(/^\d{1,2}\/\d{1,2}\/\d{4}$/);
      }
    });

    it("keeps the fixed debounce and sanity values from the contract", () => {
      expect(selectors.stabilityDebounceMs).toBe(1500);
      expect(Number.isInteger(selectors.sanityMinChars)).toBe(true);
      expect(selectors.sanityMinChars).toBeGreaterThanOrEqual(50);
    });

    it("contains no template placeholder text", () => {
      const strings: Array<{ path: string; text: string }> = [];
      collectStringValues(selectors, "lecture-page.selectors.json", strings);
      for (const { path, text } of strings) {
        expect(text, `${path} must not contain placeholder text`).not.toMatch(
          PLACEHOLDER_PATTERN,
        );
      }
    });
  });

  describe("course-mapping.json", () => {
    it("keeps page labels, slugs, terms, and slug-term pairs unique and well formed", () => {
      expect(Array.isArray(courseMapping.courseMappings)).toBe(true);
      expect(courseMapping.courseMappings.length).toBeGreaterThan(0);

      const labels = new Set<string>();
      const slugs = new Set<string>();
      const pairs = new Set<string>();
      for (const [index, entry] of courseMapping.courseMappings.entries()) {
        const label = `courseMappings[${index}]`;
        expectNonemptyString(entry.pageCourseText, `${label}.pageCourseText`);
        expectNonemptyString(entry.courseName, `${label}.courseName`);
        expect(entry.courseName).not.toContain("\n");
        expect(entry.courseName.length).toBeLessThanOrEqual(256);
        expect(entry.courseSlug, `${label}.courseSlug`).toMatch(COURSE_SLUG_PATTERN);

        expect(Array.isArray(entry.supportedTerms)).toBe(true);
        expect(entry.supportedTerms.length, `${label}.supportedTerms`).toBeGreaterThan(0);
        expect(
          new Set(entry.supportedTerms).size,
          `${label}.supportedTerms must not contain duplicates`,
        ).toBe(entry.supportedTerms.length);
        for (const term of entry.supportedTerms) {
          expect(term, `${label}.supportedTerms entry`).toMatch(TERM_PATTERN);
        }

        const normalizedLabel = normalizePageLabel(entry.pageCourseText);
        expect(
          labels.has(normalizedLabel),
          `${label} duplicates normalized page label "${normalizedLabel}"`,
        ).toBe(false);
        labels.add(normalizedLabel);

        expect(
          slugs.has(entry.courseSlug),
          `${label} duplicates courseSlug "${entry.courseSlug}"`,
        ).toBe(false);
        slugs.add(entry.courseSlug);

        for (const term of entry.supportedTerms) {
          const pair = `${entry.courseSlug}/${term}`;
          expect(pairs.has(pair), `${label} duplicates pair "${pair}"`).toBe(false);
          pairs.add(pair);
        }
      }
    });
  });

  describe("expected parser outputs", () => {
    const expectedFixtures = EXPECTED_FIXTURES.map((name) => ({
      name,
      expected: readJson<Record<string, unknown>>(name),
    }));

    it("declares supported as a boolean and covers both outcomes", () => {
      for (const { name, expected } of expectedFixtures) {
        expect(typeof expected.supported, `${name}.supported`).toBe("boolean");
      }
      expect(
        expectedFixtures.some(({ expected }) => expected.supported === true),
      ).toBe(true);
      expect(
        expectedFixtures.some(({ expected }) => expected.supported === false),
      ).toBe(true);
    });

    for (const { name, expected } of expectedFixtures) {
      it(`${name} keeps the expected-output contract`, () => {
        if (expected.supported !== true) {
          expectNonemptyString(expected.expectedRejection, `${name}.expectedRejection`);
          return;
        }

        const courseSlug = expectNonemptyString(expected.courseSlug, `${name}.courseSlug`);
        const term = expectNonemptyString(expected.term, `${name}.term`);
        expect(courseSlug).toMatch(COURSE_SLUG_PATTERN);
        expect(term).toMatch(TERM_PATTERN);

        expect(typeof expected.lectureNumber, `${name}.lectureNumber`).toBe("number");
        const lectureNumber = expected.lectureNumber as number;
        expect(Number.isInteger(lectureNumber)).toBe(true);

        const lectureKey = expectNonemptyString(expected.lectureKey, `${name}.lectureKey`);
        expect(lectureKey).toMatch(LECTURE_KEY_PATTERN);
        expect(lectureKey).toBe(
          `${courseSlug}/${term}/${String(lectureNumber).padStart(3, "0")}`,
        );

        const contentHash = expectNonemptyString(
          expected.contentHash,
          `${name}.contentHash`,
        );
        expect(contentHash).toMatch(CONTENT_HASH_PATTERN);
        expect(DERIVED_FROM_VALUES).toContain(expected.derivedFrom);

        const sourceUrl = expectNonemptyString(expected.sourceUrl, `${name}.sourceUrl`);
        expect(Buffer.byteLength(sourceUrl, "utf8")).toBeLessThanOrEqual(2048);
        const url = new URL(sourceUrl);
        expect(url.protocol).toBe("https:");
        expect(url.hostname).toBe("leccap.engin.umich.edu");
        expect(url.username).toBe("");
        expect(url.password).toBe("");
        expect(url.port).toBe("");
        expect(url.search).toBe("");
        expect(url.hash).toBe("");
        expect(url.pathname.startsWith("/")).toBe(true);

        expect(supportedPairs.has(`${courseSlug}/${term}`)).toBe(true);
      });
    }
  });

  describe("transcript-size-report.json", () => {
    it("records at least two samples with an RFC3339 capturedAt", () => {
      expect(sizeReport.capturedAt).toMatch(
        /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/,
      );
      expect(Array.isArray(sizeReport.samples)).toBe(true);
      expect(sizeReport.samples.length).toBeGreaterThanOrEqual(2);
    });

    it("keeps every sample within the byte and character caps", () => {
      for (const sample of sizeReport.samples) {
        const label = `sample "${sample.sampleId}"`;
        expectNonemptyString(sample.sampleId, `${label}.sampleId`);
        expect(sample.transcriptBytes, `${label}.transcriptBytes`).toBeLessThanOrEqual(
          460800,
        );
        expect(
          sample.timestampedTranscriptBytes,
          `${label}.timestampedTranscriptBytes`,
        ).toBeLessThanOrEqual(460800);
        expect(sample.serializedJobBytes, `${label}.serializedJobBytes`).toBeLessThanOrEqual(
          972800,
        );
        expect(sample.nativeMessageBytes, `${label}.nativeMessageBytes`).toBeLessThanOrEqual(
          972800,
        );
        expect(sample.statusPageBytes, `${label}.statusPageBytes`).toBeLessThanOrEqual(
          1048576,
        );

        expect(
          sample.renderTimeMs === null ||
            (typeof sample.renderTimeMs === "number" && sample.renderTimeMs >= 0),
          `${label}.renderTimeMs must be null or a nonnegative number`,
        ).toBe(true);
        if (typeof sample.renderTimeMs === "number") {
          expect(sample.renderTimeMs, `${label}.renderTimeMs`).toBeLessThan(30000);
        }

        for (const [key, value] of Object.entries(sample)) {
          if (typeof value !== "string") {
            continue;
          }
          if (key === "sourceUrl") {
            expect(
              Buffer.byteLength(value, "utf8"),
              `${label}.sourceUrl`,
            ).toBeLessThanOrEqual(2048);
          } else {
            expect(value.length, `${label}.${key}`).toBeLessThanOrEqual(256);
          }
        }
      }
    });

    it("keeps observedMaxima equal to the sample maxima and below the approved limits", () => {
      expect(sizeReport.approvedLimits).toEqual({
        transcriptBytes: 460800,
        timestampedTranscriptBytes: 460800,
        serializedJobBytes: 972800,
        nativeMessageBytes: 1048576,
        statusPageBytes: 1048576,
        renderTimeMs: 30000,
      });
      expect(Object.keys(sizeReport.observedMaxima).sort()).toEqual(
        Object.keys(sizeReport.approvedLimits).sort(),
      );

      for (const key of Object.keys(sizeReport.approvedLimits)) {
        const observed = sizeReport.observedMaxima[key];
        const computed = maximaFromSamples(sizeReport.samples, key);
        if (observed === null) {
          expect(key).toBe("renderTimeMs");
          expect(computed).toBeNull();
          expect(sizeReport.renderTimeMeasurement?.status).toBe(
            "pending_live_measurement",
          );
        } else {
          expect(observed).toBe(computed);
          expect(observed as number).toBeLessThan(sizeReport.approvedLimits[key]);
        }
      }
    });

    it("keeps sample identity fields consistent with the course mapping", () => {
      for (const sample of sizeReport.samples) {
        const label = `sample "${sample.sampleId}"`;
        expect(sample.courseSlug, `${label}.courseSlug`).toMatch(COURSE_SLUG_PATTERN);
        expect(sample.term, `${label}.term`).toMatch(TERM_PATTERN);
        expect(Number.isInteger(sample.lectureNumber), `${label}.lectureNumber`).toBe(
          true,
        );
        expect(sample.lectureNumber).toBeGreaterThanOrEqual(1);
        expect(supportedPairs.has(`${sample.courseSlug}/${sample.term}`)).toBe(true);
      }
    });
  });

  describe("sanitized fixture HTML", () => {
    it("contains no media or WebVTT URLs", () => {
      for (const name of HTML_FIXTURES) {
        const html = readFixture(name);
        for (const url of html.match(ABSOLUTE_URL_PATTERN) ?? []) {
          expect(url, `${name} references media URL ${url}`).not.toMatch(
            MEDIA_URL_PATTERN,
          );
        }
        for (const match of html.matchAll(ATTRIBUTE_URL_PATTERN)) {
          expect(match[1], `${name} attribute URL ${match[1]}`).not.toMatch(
            MEDIA_URL_PATTERN,
          );
        }
      }
    });

    it("contains no cookie, session, token, or user-identifier secrets", () => {
      for (const name of HTML_FIXTURES) {
        const html = readFixture(name);
        for (const [label, pattern] of SECRET_PATTERNS) {
          expect(html, `${name} must not contain ${label}`).not.toMatch(pattern);
        }
      }
    });

    it("uses only sanitized route IDs", () => {
      let routeIdCount = 0;
      for (const name of HTML_FIXTURES) {
        const html = readFixture(name);
        for (const pattern of [ROUTE_ID_PATTERN, BARE_ROUTE_ID_PATTERN]) {
          for (const match of html.matchAll(pattern)) {
            routeIdCount += 1;
            expect(
              match[1],
              `${name} route ID "${match[1]}" must be sanitized`,
            ).toMatch(SANITIZED_ROUTE_ID_PATTERN);
          }
        }
      }
      expect(routeIdCount).toBeGreaterThan(0);
    });
  });
});
