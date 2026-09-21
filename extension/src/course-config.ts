/**
 * The Stage 0 course allowlist is deliberately data, not a page-derived
 * fallback.  Adding a course or term requires a new verified Stage 0 entry.
 */

export type TermSeason = "winter" | "spring" | "summer" | "fall";

export interface CourseConfig {
  readonly pageCourseText: string;
  readonly courseName: string;
  readonly courseSlug: string;
  readonly supportedTerms: readonly string[];
}

export interface ParsedTerm {
  readonly year: number;
  readonly season: TermSeason;
  readonly normalized: string;
  readonly human: string;
}

export type CourseConfigErrorCode =
  | "invalid_course"
  | "invalid_slug"
  | "invalid_term"
  | "unsupported_course"
  | "unsupported_term"
  | "slug_collision";

export class CourseConfigError extends Error {
  readonly code: CourseConfigErrorCode;

  constructor(code: CourseConfigErrorCode, message: string) {
    super(message);
    this.name = "CourseConfigError";
    this.code = code;
  }
}

/**
 * Exact copy of extension-tests/fixtures/course-mapping.json's allowlist.
 * Keep this list intentionally small and explicit.
 */
export const COURSE_MAPPINGS: readonly CourseConfig[] = Object.freeze([
  Object.freeze({
    pageCourseText: "EECS 484",
    courseName: "EECS 484",
    courseSlug: "eecs484",
    supportedTerms: Object.freeze(["2026-fall"]),
  }),
]);

const HUMAN_TERM_RE = /^\s*(Winter|Spring|Summer|Fall)\s+(\d{4})\s*$/i;
const NORMALIZED_TERM_RE = /^\s*(\d{4})-(winter|spring|summer|fall)\s*$/i;
const COURSE_SLUG_RE = /^[a-z0-9]+$/;

/** Normalize only presentation whitespace; do not invent a course identity. */
export function normalizeCourseLabel(value: string): string {
  if (typeof value !== "string") {
    throw new CourseConfigError("invalid_course", "course label must be a string");
  }

  return value.normalize("NFC").replace(/[\t\r\n ]+/g, " ").trim();
}

/**
 * Derive the path-safe slug described by the plan.  The committed mapping is
 * still authoritative; this helper is not an allowlist lookup.
 */
export function normalizeCourseSlug(courseName: string): string {
  const label = normalizeCourseLabel(courseName);
  const slug = label.toLowerCase().replace(/[^a-z0-9]/g, "");

  if (!slug || !COURSE_SLUG_RE.test(slug)) {
    throw new CourseConfigError(
      "invalid_slug",
      "course slug must contain at least one lowercase ASCII alphanumeric character",
    );
  }

  return slug;
}

function seasonLabel(season: TermSeason): string {
  return season[0].toUpperCase() + season.slice(1);
}

/** Parse either the human or normalized term representation. */
export function parseTerm(value: string): ParsedTerm {
  if (typeof value !== "string") {
    throw new CourseConfigError("invalid_term", "term must be a string");
  }

  const humanMatch = HUMAN_TERM_RE.exec(value);
  if (humanMatch) {
    const season = humanMatch[1].toLowerCase() as TermSeason;
    const year = Number(humanMatch[2]);
    return {
      year,
      season,
      normalized: `${year}-${season}`,
      human: `${seasonLabel(season)} ${year}`,
    };
  }

  const normalizedMatch = NORMALIZED_TERM_RE.exec(value);
  if (normalizedMatch) {
    const year = Number(normalizedMatch[1]);
    const season = normalizedMatch[2].toLowerCase() as TermSeason;
    return {
      year,
      season,
      normalized: `${year}-${season}`,
      human: `${seasonLabel(season)} ${year}`,
    };
  }

  throw new CourseConfigError(
    "invalid_term",
    "term must be a single Winter, Spring, Summer, or Fall term with a four-digit year",
  );
}

export function normalizeTerm(value: string): string {
  return parseTerm(value).normalized;
}

export function humanizeTerm(value: string): string {
  return parseTerm(value).human;
}

function assertCourseMappings(): void {
  const seenSlugs = new Map<string, string>();

  for (const mapping of COURSE_MAPPINGS) {
    const pageCourseText = normalizeCourseLabel(mapping.pageCourseText);
    const courseName = normalizeCourseLabel(mapping.courseName);

    if (!pageCourseText || !courseName) {
      throw new CourseConfigError("invalid_course", "course mapping labels may not be empty");
    }
    if (!COURSE_SLUG_RE.test(mapping.courseSlug)) {
      throw new CourseConfigError(
        "invalid_slug",
        `course mapping slug is not path-safe: ${mapping.courseSlug}`,
      );
    }
    if (normalizeCourseSlug(courseName) !== mapping.courseSlug) {
      throw new CourseConfigError(
        "invalid_slug",
        `course mapping slug does not match its course name: ${courseName}`,
      );
    }
    if (mapping.supportedTerms.length === 0) {
      throw new CourseConfigError(
        "unsupported_term",
        `course mapping has no supported terms: ${courseName}`,
      );
    }

    const priorCourseName = seenSlugs.get(mapping.courseSlug);
    if (priorCourseName && priorCourseName !== courseName) {
      throw new CourseConfigError(
        "slug_collision",
        `distinct course names share course slug ${mapping.courseSlug}`,
      );
    }
    seenSlugs.set(mapping.courseSlug, courseName);

    const seenTerms = new Set<string>();
    for (const term of mapping.supportedTerms) {
      const normalized = normalizeTerm(term);
      if (seenTerms.has(normalized)) {
        throw new CourseConfigError(
          "unsupported_term",
          `course mapping repeats supported term ${normalized}`,
        );
      }
      seenTerms.add(normalized);
    }
  }
}

assertCourseMappings();

/** Return the exact allowlisted course/term mapping, or fail closed. */
export function getCourseMapping(courseText: string, term: string): CourseConfig {
  const normalizedCourse = normalizeCourseLabel(courseText);
  const normalizedTerm = normalizeTerm(term);
  const mapping = COURSE_MAPPINGS.find(
    (candidate) =>
      (candidate.pageCourseText === normalizedCourse || candidate.courseName === normalizedCourse) &&
      candidate.supportedTerms.some((supportedTerm) => normalizeTerm(supportedTerm) === normalizedTerm),
  );

  if (mapping) {
    return mapping;
  }

  const knownCourse = COURSE_MAPPINGS.some(
    (candidate) =>
      candidate.pageCourseText === normalizedCourse || candidate.courseName === normalizedCourse,
  );
  if (!knownCourse) {
    throw new CourseConfigError(
      "unsupported_course",
      `course is not in the Stage 0 allowlist: ${normalizedCourse}`,
    );
  }

  throw new CourseConfigError(
    "unsupported_term",
    `term ${normalizedTerm} is not supported for ${normalizedCourse}`,
  );
}

export function isSupportedCourseTerm(courseText: string, term: string): boolean {
  try {
    getCourseMapping(courseText, term);
    return true;
  } catch (error) {
    if (error instanceof CourseConfigError) {
      return false;
    }
    throw error;
  }
}

/** Validate all identity fields against the committed allowlist. */
export function matchesCourseIdentity(
  courseName: string,
  courseSlug: string,
  term: string,
): boolean {
  try {
    const mapping = getCourseMapping(courseName, term);
    return mapping.courseName === normalizeCourseLabel(courseName) && mapping.courseSlug === courseSlug;
  } catch (error) {
    if (error instanceof CourseConfigError) {
      return false;
    }
    throw error;
  }
}
