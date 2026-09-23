/**
 * Persisted course allowlist.
 *
 * The options page writes a validated `courseMappings` array to
 * `chrome.storage.local`.  The content runtime prefers stored mappings and
 * falls back to the built-in Stage 0 allowlist when storage is empty,
 * invalid, or unavailable (including under Vitest, where `chrome` is
 * undefined).  Validation deliberately mirrors `course-config.ts`: a stored
 * entry can never widen the identity rules beyond the committed contract.
 */

import {
  COURSE_MAPPINGS,
  normalizeCourseLabel,
  type CourseConfig,
} from "./course-config";

export const COURSE_MAPPINGS_STORAGE_KEY = "courseMappings";

const COURSE_SLUG_RE = /^[a-z0-9]+$/;
const TERM_RE = /^\d{4}-(winter|spring|summer|fall)$/;
const PREFERRED_SECTION_RE = /^\d{3}$/;
const NEWLINE_RE = /[\r\n]/;
const MAX_COURSE_NAME_LENGTH = 256;

export interface CourseStorageArea {
  get(keys?: string | string[] | null): Promise<Record<string, unknown>>;
  set(items: Record<string, unknown>): Promise<void>;
}

export interface CourseMappingIssue {
  /** Zero-based entry index, or null for a whole-array problem. */
  index: number | null;
  /** Field name, or null for an entry/array problem. */
  field: string | null;
  message: string;
}

export type CourseMappingsValidation =
  | { ok: true; mappings: CourseConfig[] }
  | { ok: false; issues: CourseMappingIssue[] };

export class CourseMappingsValidationError extends Error {
  readonly issues: CourseMappingIssue[];

  constructor(issues: CourseMappingIssue[]) {
    super("courseMappings failed validation");
    this.name = "CourseMappingsValidationError";
    this.issues = issues;
  }
}

export function defaultCourseStorageArea(): CourseStorageArea | null {
  const chromeApi = (
    globalThis as { chrome?: { storage?: { local?: CourseStorageArea } } }
  ).chrome;
  return chromeApi?.storage?.local ?? null;
}

function issue(
  index: number | null,
  field: string | null,
  message: string,
): CourseMappingIssue {
  return { index, field, message };
}

function entryLabel(index: number): string {
  return `course ${index + 1}`;
}

function validateEntry(
  value: unknown,
  index: number,
  issues: CourseMappingIssue[],
): CourseConfig | null {
  const label = entryLabel(index);
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    issues.push(issue(index, null, `${label} must be an object`));
    return null;
  }
  const entry = value as Record<string, unknown>;
  let valid = true;
  const fail = (field: string, message: string): void => {
    issues.push(issue(index, field, `${label}: ${message}`));
    valid = false;
  };

  const pageCourseText = entry.pageCourseText;
  if (typeof pageCourseText !== "string" || normalizeCourseLabel(pageCourseText) === "") {
    fail("pageCourseText", "pageCourseText must be a nonempty string");
  } else if (NEWLINE_RE.test(pageCourseText)) {
    fail("pageCourseText", "pageCourseText must not contain a newline");
  }

  const courseName = entry.courseName;
  if (typeof courseName !== "string" || normalizeCourseLabel(courseName) === "") {
    fail("courseName", "courseName must be a nonempty string");
  } else if (NEWLINE_RE.test(courseName)) {
    fail("courseName", "courseName must not contain a newline");
  } else if (courseName.length > MAX_COURSE_NAME_LENGTH) {
    fail("courseName", `courseName must be at most ${MAX_COURSE_NAME_LENGTH} characters`);
  }

  const courseSlug = entry.courseSlug;
  if (typeof courseSlug !== "string" || !COURSE_SLUG_RE.test(courseSlug)) {
    fail("courseSlug", "courseSlug must match ^[a-z0-9]+$");
  }

  const supportedTerms = entry.supportedTerms;
  if (!Array.isArray(supportedTerms) || supportedTerms.length === 0) {
    fail("supportedTerms", "supportedTerms must be a nonempty array");
  } else {
    const seenTerms = new Set<string>();
    for (const term of supportedTerms) {
      if (typeof term !== "string" || !TERM_RE.test(term)) {
        fail("supportedTerms", `supportedTerms entry must match ^\\d{4}-(winter|spring|summer|fall)$: ${String(term)}`);
        continue;
      }
      if (seenTerms.has(term)) {
        fail("supportedTerms", `supportedTerms repeats ${term}`);
      }
      seenTerms.add(term);
    }
  }

  const preferred = entry.preferredDiscussionSection;
  if (preferred !== undefined && preferred !== null && preferred !== "") {
    if (typeof preferred !== "string" || !PREFERRED_SECTION_RE.test(preferred)) {
      fail(
        "preferredDiscussionSection",
        "preferredDiscussionSection must be empty or match ^\\d{3}$",
      );
    }
  }

  if (!valid) return null;
  return {
    pageCourseText: pageCourseText as string,
    courseName: courseName as string,
    courseSlug: courseSlug as string,
    supportedTerms: [...(supportedTerms as string[])],
    preferredDiscussionSection:
      typeof preferred === "string" && preferred !== "" ? preferred : null,
  };
}

/** Validate an untrusted `courseMappings` value without mutating it. */
export function validateCourseMappings(value: unknown): CourseMappingsValidation {
  if (!Array.isArray(value)) {
    return {
      ok: false,
      issues: [issue(null, null, "courseMappings must be an array")],
    };
  }

  const issues: CourseMappingIssue[] = [];
  const mappings: CourseConfig[] = [];
  const seenLabels = new Map<string, number>();
  const seenSlugs = new Map<string, number>();
  const seenPairs = new Map<string, number>();

  value.forEach((entry, index) => {
    const mapping = validateEntry(entry, index, issues);
    if (!mapping) return;

    const normalizedLabel = normalizeCourseLabel(mapping.pageCourseText);
    const priorLabel = seenLabels.get(normalizedLabel);
    if (priorLabel !== undefined) {
      issues.push(
        issue(
          index,
          "pageCourseText",
          `${entryLabel(index)} duplicates the normalized page label of course ${priorLabel + 1}: ${normalizedLabel}`,
        ),
      );
      return;
    }

    const priorSlug = seenSlugs.get(mapping.courseSlug);
    if (priorSlug !== undefined) {
      issues.push(
        issue(
          index,
          "courseSlug",
          `${entryLabel(index)} duplicates courseSlug ${mapping.courseSlug} from course ${priorSlug + 1}`,
        ),
      );
      return;
    }

    const duplicatedPair = mapping.supportedTerms
      .map((term) => `${mapping.courseSlug}/${term}`)
      .find((pair) => seenPairs.has(pair));
    if (duplicatedPair) {
      issues.push(
        issue(
          index,
          "supportedTerms",
          `${entryLabel(index)} duplicates the courseSlug/term pair ${duplicatedPair}`,
        ),
      );
      return;
    }

    seenLabels.set(normalizedLabel, index);
    seenSlugs.set(mapping.courseSlug, index);
    for (const term of mapping.supportedTerms) {
      seenPairs.set(`${mapping.courseSlug}/${term}`, index);
    }
    mappings.push(mapping);
  });

  if (issues.length > 0) {
    return { ok: false, issues };
  }
  return { ok: true, mappings };
}

/**
 * Read the stored allowlist when it is present and valid.  Returns null when
 * storage is unavailable, unset, unreadable, or holds an invalid value.
 */
export async function loadStoredCourseMappings(
  area?: CourseStorageArea | null,
): Promise<CourseMappingsValidation | null> {
  const target = area === undefined ? defaultCourseStorageArea() : area;
  if (!target) return null;

  let result: Record<string, unknown>;
  try {
    result = await target.get(COURSE_MAPPINGS_STORAGE_KEY);
  } catch {
    return null;
  }

  const value = result?.[COURSE_MAPPINGS_STORAGE_KEY];
  if (value === undefined || value === null) return null;
  return validateCourseMappings(value);
}

/**
 * The effective allowlist for the content runtime: the stored mappings when
 * present and valid, otherwise the built-in Stage 0 allowlist.
 */
export async function loadCourseMappings(
  area?: CourseStorageArea | null,
): Promise<readonly CourseConfig[]> {
  const stored = await loadStoredCourseMappings(area);
  if (stored?.ok && stored.mappings.length > 0) {
    return stored.mappings;
  }
  return COURSE_MAPPINGS;
}

/** Validate, then persist the allowlist.  Invalid data is never written. */
export async function saveCourseMappings(
  mappings: unknown,
  area?: CourseStorageArea | null,
): Promise<CourseConfig[]> {
  const validation = validateCourseMappings(mappings);
  if (!validation.ok) {
    throw new CourseMappingsValidationError(validation.issues);
  }
  const target = area === undefined ? defaultCourseStorageArea() : area;
  if (!target) {
    throw new Error("chrome.storage.local is unavailable");
  }
  await target.set({ [COURSE_MAPPINGS_STORAGE_KEY]: validation.mappings });
  return validation.mappings;
}
