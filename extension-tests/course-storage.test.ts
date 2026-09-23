import { afterEach, describe, expect, it } from "vitest";

import { COURSE_MAPPINGS } from "../extension/src/course-config";
import {
  COURSE_MAPPINGS_STORAGE_KEY,
  CourseMappingsValidationError,
  loadCourseMappings,
  loadStoredCourseMappings,
  saveCourseMappings,
  validateCourseMappings,
  type CourseStorageArea,
} from "../extension/src/course-storage";

class MemoryStorageArea implements CourseStorageArea {
  readonly values = new Map<string, unknown>();
  failOnGet = false;

  async get(keys?: string | string[] | null): Promise<Record<string, unknown>> {
    if (this.failOnGet) throw new Error("storage read failed");
    if (keys === undefined || keys === null) {
      return Object.fromEntries(this.values);
    }
    const list = Array.isArray(keys) ? keys : [keys];
    const result: Record<string, unknown> = {};
    for (const key of list) {
      if (this.values.has(key)) result[key] = this.values.get(key);
    }
    return result;
  }

  async set(items: Record<string, unknown>): Promise<void> {
    for (const [key, value] of Object.entries(items)) {
      this.values.set(key, structuredClone(value));
    }
  }
}

function validEntry(
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    pageCourseText: "EECS 484",
    courseName: "EECS 484",
    courseSlug: "eecs484",
    supportedTerms: ["2026-fall"],
    ...overrides,
  };
}

function setChromeStorage(area: CourseStorageArea | null): void {
  (globalThis as Record<string, unknown>).chrome = area
    ? { storage: { local: area } }
    : {};
}

afterEach(() => {
  delete (globalThis as Record<string, unknown>).chrome;
});

describe("course mapping validation", () => {
  it("accepts the committed built-in allowlist shape", () => {
    const result = validateCourseMappings(COURSE_MAPPINGS);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.mappings).toHaveLength(COURSE_MAPPINGS.length);
    expect(result.mappings[0]).toMatchObject({
      pageCourseText: "EECS 484",
      courseSlug: "eecs484",
      supportedTerms: ["2026-fall"],
      preferredDiscussionSection: null,
    });
  });

  it("accepts empty, null, and three-digit preferred discussion sections", () => {
    const entries = [
      validEntry({ preferredDiscussionSection: undefined }),
      validEntry({
        pageCourseText: "EECS 491",
        courseName: "EECS 491",
        courseSlug: "eecs491",
        preferredDiscussionSection: null,
      }),
      validEntry({
        pageCourseText: "EECS 492",
        courseName: "EECS 492",
        courseSlug: "eecs492",
        preferredDiscussionSection: "",
      }),
      validEntry({
        pageCourseText: "EECS 493",
        courseName: "EECS 493",
        courseSlug: "eecs493",
        preferredDiscussionSection: "012",
      }),
    ];
    const result = validateCourseMappings(entries);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.mappings.map((entry) => entry.preferredDiscussionSection)).toEqual([
      null,
      null,
      null,
      "012",
    ]);
  });

  it("accepts multiple normalized terms for one course", () => {
    const result = validateCourseMappings([
      validEntry({ supportedTerms: ["2026-fall", "2027-winter"] }),
    ]);
    expect(result.ok).toBe(true);
  });

  it("accepts an empty array but the runtime treats it as unset", async () => {
    const validation = validateCourseMappings([]);
    expect(validation).toEqual({ ok: true, mappings: [] });

    const area = new MemoryStorageArea();
    area.values.set(COURSE_MAPPINGS_STORAGE_KEY, []);
    await expect(loadCourseMappings(area)).resolves.toBe(COURSE_MAPPINGS);
  });

  it("rejects a non-array value", () => {
    expect(validateCourseMappings({ courseMappings: [] }).ok).toBe(false);
    expect(validateCourseMappings("courseMappings").ok).toBe(false);
    expect(validateCourseMappings(null).ok).toBe(false);
  });

  it("rejects non-object entries and empty or newline-bearing labels", () => {
    expect(validateCourseMappings(["EECS 484"]).ok).toBe(false);
    expect(validateCourseMappings([validEntry({ pageCourseText: "" })]).ok).toBe(false);
    expect(validateCourseMappings([validEntry({ pageCourseText: "   " })]).ok).toBe(false);
    expect(validateCourseMappings([validEntry({ courseName: "" })]).ok).toBe(false);
    expect(
      validateCourseMappings([validEntry({ pageCourseText: "EECS\n484" })]).ok,
    ).toBe(false);
    expect(
      validateCourseMappings([validEntry({ courseName: "EECS 484\n" })]).ok,
    ).toBe(false);
  });

  it("rejects an over-long course name", () => {
    const result = validateCourseMappings([
      validEntry({ courseName: "E".repeat(257) }),
    ]);
    expect(result.ok).toBe(false);
    if (result.ok) return;
    expect(result.issues[0].field).toBe("courseName");
  });

  it("rejects slugs that do not match ^[a-z0-9]+$", () => {
    for (const courseSlug of ["EECS484", "eecs-484", "eecs 484", "", "eecs.484"]) {
      const result = validateCourseMappings([validEntry({ courseSlug })]);
      expect(result.ok, `slug ${JSON.stringify(courseSlug)}`).toBe(false);
    }
  });

  it("rejects empty, malformed, or duplicated supported terms", () => {
    for (const supportedTerms of [
      [],
      "2026-fall",
      ["Fall 2026"],
      ["2026-Fall"],
      ["26-fall"],
      ["2026-fall", "2026-fall"],
      ["2026-fall", 42],
    ]) {
      const result = validateCourseMappings([validEntry({ supportedTerms })]);
      expect(result.ok, `terms ${JSON.stringify(supportedTerms)}`).toBe(false);
    }
  });

  it("rejects duplicate normalized page labels, slugs, and slug/term pairs", () => {
    const duplicateLabel = validateCourseMappings([
      validEntry(),
      validEntry({
        pageCourseText: "EECS\t484",
        courseName: "EECS 484 Section Two",
        courseSlug: "eecs484b",
      }),
    ]);
    expect(duplicateLabel.ok).toBe(false);

    const duplicateSlug = validateCourseMappings([
      validEntry(),
      validEntry({
        pageCourseText: "EECS 485",
        courseName: "EECS 485",
        courseSlug: "eecs484",
      }),
    ]);
    expect(duplicateSlug.ok).toBe(false);

    const duplicatePair = validateCourseMappings([
      validEntry(),
      validEntry({
        pageCourseText: "EECS 486",
        courseName: "EECS 486",
        courseSlug: "eecs484",
        supportedTerms: ["2026-fall"],
      }),
    ]);
    expect(duplicatePair.ok).toBe(false);
  });

  it("rejects malformed preferred discussion sections", () => {
    for (const preferredDiscussionSection of ["12", "0123", "abc", "0 12", 12]) {
      const result = validateCourseMappings([
        validEntry({ preferredDiscussionSection }),
      ]);
      expect(
        result.ok,
        `preferred ${JSON.stringify(preferredDiscussionSection)}`,
      ).toBe(false);
    }
  });
});

describe("course mapping storage", () => {
  it("saves validated mappings under the courseMappings key and reloads them", async () => {
    const area = new MemoryStorageArea();
    const saved = await saveCourseMappings(
      [validEntry({ preferredDiscussionSection: "012" })],
      area,
    );

    expect(saved).toHaveLength(1);
    expect(area.values.get(COURSE_MAPPINGS_STORAGE_KEY)).toMatchObject([
      { courseSlug: "eecs484", preferredDiscussionSection: "012" },
    ]);

    const loaded = await loadCourseMappings(area);
    expect(loaded).toHaveLength(1);
    expect(loaded[0]).toMatchObject({
      courseName: "EECS 484",
      preferredDiscussionSection: "012",
    });
  });

  it("never writes invalid mappings", async () => {
    const area = new MemoryStorageArea();
    await expect(
      saveCourseMappings([validEntry({ courseSlug: "EECS 484" })], area),
    ).rejects.toBeInstanceOf(CourseMappingsValidationError);
    expect(area.values.has(COURSE_MAPPINGS_STORAGE_KEY)).toBe(false);
  });

  it("falls back to the built-in allowlist when storage is unavailable", async () => {
    await expect(loadCourseMappings(undefined)).resolves.toBe(COURSE_MAPPINGS);
    await expect(loadCourseMappings(null)).resolves.toBe(COURSE_MAPPINGS);

    setChromeStorage(null);
    await expect(loadCourseMappings()).resolves.toBe(COURSE_MAPPINGS);
    await expect(saveCourseMappings([validEntry()])).rejects.toThrow(
      "chrome.storage.local is unavailable",
    );
  });

  it("falls back to the built-in allowlist when storage is empty or invalid", async () => {
    const empty = new MemoryStorageArea();
    await expect(loadCourseMappings(empty)).resolves.toBe(COURSE_MAPPINGS);

    const invalid = new MemoryStorageArea();
    invalid.values.set(COURSE_MAPPINGS_STORAGE_KEY, [
      validEntry({ courseSlug: "EECS 484" }),
    ]);
    await expect(loadCourseMappings(invalid)).resolves.toBe(COURSE_MAPPINGS);
    await expect(loadStoredCourseMappings(invalid)).resolves.toMatchObject({
      ok: false,
    });

    const unreadable = new MemoryStorageArea();
    unreadable.failOnGet = true;
    await expect(loadCourseMappings(unreadable)).resolves.toBe(COURSE_MAPPINGS);
    await expect(loadStoredCourseMappings(unreadable)).resolves.toBeNull();
  });

  it("uses the stored mappings when chrome.storage.local holds a valid list", async () => {
    const area = new MemoryStorageArea();
    area.values.set(COURSE_MAPPINGS_STORAGE_KEY, [
      validEntry({ supportedTerms: ["2027-winter"], preferredDiscussionSection: "003" }),
    ]);
    setChromeStorage(area);

    const loaded = await loadCourseMappings();
    expect(loaded).toHaveLength(1);
    expect(loaded[0]).toMatchObject({
      supportedTerms: ["2027-winter"],
      preferredDiscussionSection: "003",
    });
  });
});
