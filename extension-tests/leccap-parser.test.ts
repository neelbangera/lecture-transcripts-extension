import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";

import {
  type CourseMapping,
  type OverviewFetcher,
  type SelectorFixture,
  canonicalizeLeccapUrl,
  parseLecturePage,
  parseRecordingDate,
} from "../extension/src/leccap-parser";

const fixtureDirectory = fileURLToPath(new URL("./fixtures/", import.meta.url));
const selectors = JSON.parse(
  readFileSync(`${fixtureDirectory}lecture-page.selectors.json`, "utf8"),
) as SelectorFixture;
const courseFixture = JSON.parse(
  readFileSync(`${fixtureDirectory}course-mapping.json`, "utf8"),
) as { courseMappings: CourseMapping[] };
const expected = JSON.parse(
  readFileSync(`${fixtureDirectory}lecture-page.expected.json`, "utf8"),
) as Record<string, unknown>;

const lectureUrl = "https://leccap.engin.umich.edu/leccap/player/r/sanitized01";
const overviewUrl =
  "https://leccap.engin.umich.edu/leccap/site/sanitizedoverview";

function readFixture(name: string): string {
  return readFileSync(`${fixtureDirectory}${name}`, "utf8");
}

function makeDocument(
  html: string,
  url = lectureUrl,
): { document: Document; dom: JSDOM } {
  const dom = new JSDOM(html, { url });
  return { document: dom.window.document, dom };
}

function fixtureFetcher(
  html: string,
  calls: Array<{ url: string; init?: RequestInit }> = [],
): OverviewFetcher {
  return async (url, init) => {
    calls.push({ url, init });
    return {
      ok: true,
      text: async () => html,
    };
  };
}

async function parseFixture(
  fixtureName = "lecture-page.html",
  sourceUrl = lectureUrl,
  fetchOverview: OverviewFetcher = fixtureFetcher(readFixture("overview-page.html")),
) {
  const { document } = makeDocument(readFixture(fixtureName), sourceUrl);
  return parseLecturePage(document, {
    selectors,
    courseMappings: courseFixture.courseMappings,
    sourceUrl,
    fetchOverview,
  });
}

describe("Leccap parser against the Stage 0 packet", () => {
  it("extracts the known course, term, number, date, rows, key, and hash", async () => {
    const result = await parseFixture();

    expect(result).toMatchObject({
      supported: expected.supported,
      completion: expected.completion,
      courseName: expected.courseName,
      courseSlug: expected.courseSlug,
      term: expected.term,
      lectureNumber: expected.lectureNumber,
      lectureDate: expected.lectureDate,
      sourceUrl: expected.sourceUrl,
      transcript: expected.transcript,
      timestampedTranscript: expected.timestampedTranscript,
      derivedFrom: expected.derivedFrom,
      lectureKey: expected.lectureKey,
      contentHash: expected.contentHash,
      stableSnapshotCount: expected.stableSnapshotCount,
    });
  });

  it("fetches only the linked overview and correlates exactly one canonical player href", async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = [];
    const result = await parseFixture(
      "lecture-page.html",
      `${lectureUrl}?session=fixture-only#ignored`,
      fixtureFetcher(readFixture("overview-page.html"), calls),
    );

    expect(result).toMatchObject({ supported: true, lectureDate: "2026-09-01" });
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe(overviewUrl);
    expect(calls[0].init).toMatchObject({ credentials: "include" });
  });

  it("rejects zero and multiple overview player-link matches", async () => {
    const noMatchOverview = readFixture("overview-page.html").replace(
      "https://leccap.engin.umich.edu/leccap/player/r/sanitized01",
      "https://leccap.engin.umich.edu/leccap/player/r/sanitized99",
    );
    const noMatch = await parseFixture(
      "lecture-page.html",
      lectureUrl,
      fixtureFetcher(noMatchOverview),
    );
    expect(noMatch).toMatchObject({
      supported: false,
      status: "rejected_ambiguous_metadata",
    });

    const duplicateOverview = readFixture("overview-page.html").replace(
      "https://leccap.engin.umich.edu/leccap/player/r/sanitized02",
      lectureUrl,
    );
    const multipleMatches = await parseFixture(
      "lecture-page.html",
      lectureUrl,
      fixtureFetcher(duplicateOverview),
    );
    expect(multipleMatches).toMatchObject({
      supported: false,
      status: "rejected_ambiguous_metadata",
    });
    expect((multipleMatches as { reason: string }).reason).toContain("2 player-link matches");
  });

  it("fails closed when the linked overview cannot be fetched", async () => {
    const failedFetcher: OverviewFetcher = async () => ({
      ok: false,
      text: async () => "",
    });
    await expect(parseFixture("lecture-page.html", lectureUrl, failedFetcher)).resolves.toMatchObject({
      supported: false,
      status: "rejected_ambiguous_metadata",
    });
  });

  it("reports an unauthenticated overview fetch instead of zero matches", async () => {
    const signInFetcher = fixtureFetcher(
      "<!doctype html><html><body><h1>Sign in to continue</h1></body></html>",
    );
    const result = await parseFixture("lecture-page.html", lectureUrl, signInFetcher);
    expect(result).toMatchObject({
      supported: false,
      status: "rejected_ambiguous_metadata",
    });
    expect((result as { reason: string }).reason).toContain("sign-in page");
  });

  it("uses only the numeric recording-title prefix, never the overview badge", async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = [];
    const result = await parseFixture(
      "no-number-lecture-page.html",
      "https://leccap.engin.umich.edu/leccap/player/r/sanitized17",
      fixtureFetcher(readFixture("overview-page.html"), calls),
    );

    expect(result).toMatchObject({
      supported: false,
      completion: "complete",
      status: "rejected_ambiguous_metadata",
      courseName: "EECS 484",
      term: "2026-fall",
      lectureNumber: null,
    });
    expect((result as { reason: string }).reason).toContain("overview badge is not an identity");
    expect(calls).toHaveLength(0);
  });

  it("rejects an unrelated page and a loading-only transcript", async () => {
    const unrelated = await parseFixture("non-lecture-page.html");
    expect(unrelated).toMatchObject({
      supported: false,
      status: "rejected_missing_identity",
    });

    const loading = await parseFixture("loading-transcript-page.html");
    expect(loading).toMatchObject({
      supported: false,
      completion: "not_ready",
      status: "not_ready",
    });
  });

  it("rejects an unmapped course/term rather than guessing a repository path", async () => {
    const { document } = makeDocument(
      readFixture("lecture-page.html").replace(
        "<span>EECS 484 - Fall 2026</span>",
        "<span>EECS 485 - Fall 2026</span>",
      ),
    );
    const result = await parseLecturePage(document, {
      selectors,
      courseMappings: courseFixture.courseMappings,
      sourceUrl: lectureUrl,
      fetchOverview: fixtureFetcher(readFixture("overview-page.html")),
    });
    expect(result).toMatchObject({
      supported: false,
      status: "rejected_ambiguous_metadata",
      courseName: "EECS 485",
      term: "2026-fall",
    });
  });

  it("rejects an unsafe source URL before any metadata or overview lookup", async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = [];
    const result = await parseFixture(
      "lecture-page.html",
      "https://example.invalid/player/sanitized01",
      fixtureFetcher(readFixture("overview-page.html"), calls),
    );
    expect(result).toMatchObject({
      supported: false,
      status: "rejected_unsafe_url",
    });
    expect(calls).toHaveLength(0);
  });

  it("does not invent timestamps when rows provide plain text only", async () => {
    const { document } = makeDocument(readFixture("lecture-page.html"));
    document.querySelectorAll(".transcript-time").forEach((element) => element.remove());
    const result = await parseLecturePage(document, {
      selectors,
      courseMappings: courseFixture.courseMappings,
      sourceUrl: lectureUrl,
      fetchOverview: fixtureFetcher(readFixture("overview-page.html")),
    });
    expect(result).toMatchObject({
      supported: true,
      derivedFrom: "plain-only",
      timestampedTranscript: "",
    });
  });

  it("rejects a mixed or malformed timestamped row shape", async () => {
    const { document } = makeDocument(readFixture("lecture-page.html"));
    const times = document.querySelectorAll(".transcript-time");
    times[0].textContent = "00:00.5";
    const malformed = await parseLecturePage(document, {
      selectors,
      courseMappings: courseFixture.courseMappings,
      sourceUrl: lectureUrl,
      fetchOverview: fixtureFetcher(readFixture("overview-page.html")),
    });
    expect(malformed).toMatchObject({
      supported: false,
      status: "rejected_ambiguous_metadata",
    });

    const { document: mixedDocument } = makeDocument(readFixture("lecture-page.html"));
    mixedDocument.querySelector(".transcript-time")?.remove();
    const mixed = await parseLecturePage(mixedDocument, {
      selectors,
      courseMappings: courseFixture.courseMappings,
      sourceUrl: lectureUrl,
      fetchOverview: fixtureFetcher(readFixture("overview-page.html")),
    });
    expect(mixed).toMatchObject({
      supported: false,
      status: "rejected_ambiguous_metadata",
    });
  });

  it("accepts a textual month/day only with the validated term year", () => {
    const textualDateRegex = "^\\s*(.+?)\\s*(?:•|$)";
    expect(parseRecordingDate("Feb 12", textualDateRegex, 1, "2026-fall")).toBe(
      "2026-02-12",
    );
    expect(parseRecordingDate("2/12", "^\\s*(.+?)$", 1, "2026-fall")).toBeNull();
    expect(parseRecordingDate("2/30/2026", "^\\s*(.+?)$", 1, "2026-fall")).toBeNull();
  });

  it("canonicalizes source URLs without allowing query or fragment identity drift", () => {
    expect(
      canonicalizeLeccapUrl(`${lectureUrl}?token=redacted#fragment`),
    ).toBe(lectureUrl);
    expect(canonicalizeLeccapUrl("http://leccap.engin.umich.edu/player")).toBeNull();
    expect(canonicalizeLeccapUrl("https://evil.example/player")).toBeNull();
  });
});

