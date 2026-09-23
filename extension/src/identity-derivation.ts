/**
 * Pure numeric-identity rules for Leccap recording titles and the linked
 * overview sequence.
 *
 * The recording title is authoritative when it carries an explicit number.
 * A lagging title ("Lecture recorded on 9/17/2026") takes its number from the
 * already-linked overview inventory by "next from the last one". The overview
 * category badge is a category/section label only and is never a number.
 *
 * No DOM and no I/O: the parser inventories the overview cards and calls in
 * here so the derivation rule stays unit-testable and single-sourced.
 */

export type RecordingKind = "lecture" | "discussion";

export interface OverviewCardFact {
  /** Canonicalized player href for exact correlation with the current page. */
  readonly href: string;
  /** Normalized recording-title text. */
  readonly title: string;
  /** Parsed rec-date as YYYY-MM-DD, or null when unparseable. */
  readonly lectureDate: string | null;
  /** Parsed rec-date start time as HH:MM 24h, or null when absent. */
  readonly recTime: string | null;
  /** Badge category: "Lecture - *" / "Discussion - *"; anything else is null. */
  readonly kind: RecordingKind | null;
  /** Badge section digits for "Discussion - 0NN"; never an identity field. */
  readonly discussionSection: string | null;
}

export type TitleIdentity =
  | { readonly kind: RecordingKind; readonly lectureNumber: number; readonly numberSource: "title" }
  | {
      readonly kind: RecordingKind;
      readonly lectureNumber: null;
      readonly numberSource: "derived";
    }
  | null;

const NUMERIC_TITLE_PREFIX = /^\s*(\d{1,3})\s+/;
const LECTURE_NUMBER_TITLE = /^\s*lecture\s*:?\s*(\d{1,3})\b/i;
const DISCUSSION_NUMBER_TITLE = /^\s*discussion\s*:?\s*(\d{1,3})\b/i;
/** Observed lag form ("Lecture recorded on 9/17/2026"), updated later to "Lecture: N". */
const LAGGING_RECORDED_TITLE = /^\s*(lecture|discussion)\s+recorded\s+on\s+/i;
const DISCUSSION_SECTION_BADGE_RE = /^\s*discussion\s*-\s*(\d{3})\s*$/i;
const LECTURE_CATEGORY_BADGE_RE = /^\s*lecture\b/i;
const DISCUSSION_CATEGORY_BADGE_RE = /^\s*discussion\b/i;
/** Observed rec-date tail ("9/1/2026 • 4:29 PM"). */
const REC_TIME_RE = /\u2022\s*(\d{1,2}):(\d{2})\s*(am|pm)\b/i;

function asNumber(raw: string | undefined): number | null {
  if (raw === undefined) return null;
  const value = Number(raw);
  return Number.isInteger(value) && value >= 1 && value <= 999 ? value : null;
}

/**
 * Parse a recording title into an explicit number, a lagging unnumbered form
 * that needs overview derivation, or null for unrecognized/decoy titles.
 */
export function parseTitleIdentity(title: string): TitleIdentity {
  const text = title.normalize("NFC").replace(/[\t\r\n ]+/g, " ").trim();
  if (text === "") return null;

  const numeric = NUMERIC_TITLE_PREFIX.exec(text);
  const numericValue = asNumber(numeric?.[1]);
  if (numericValue !== null) {
    return { kind: "lecture", lectureNumber: numericValue, numberSource: "title" };
  }

  const discussion = DISCUSSION_NUMBER_TITLE.exec(text);
  const discussionValue = asNumber(discussion?.[1]);
  if (discussionValue !== null) {
    return { kind: "discussion", lectureNumber: discussionValue, numberSource: "title" };
  }

  const lecture = LECTURE_NUMBER_TITLE.exec(text);
  const lectureValue = asNumber(lecture?.[1]);
  if (lectureValue !== null) {
    return { kind: "lecture", lectureNumber: lectureValue, numberSource: "title" };
  }

  const lagging = LAGGING_RECORDED_TITLE.exec(text);
  if (lagging) {
    const kind: RecordingKind = lagging[1].toLowerCase() === "discussion" ? "discussion" : "lecture";
    return { kind, lectureNumber: null, numberSource: "derived" };
  }

  return null;
}

/** Badge category and optional discussion section; the badge number is never an identity. */
export function parseRecordingBadge(badgeText: string): {
  kind: RecordingKind | null;
  discussionSection: string | null;
} {
  const text = badgeText.normalize("NFC").replace(/[\t\r\n ]+/g, " ").trim();
  const section = DISCUSSION_SECTION_BADGE_RE.exec(text);
  if (section) return { kind: "discussion", discussionSection: section[1] };
  if (LECTURE_CATEGORY_BADGE_RE.test(text)) return { kind: "lecture", discussionSection: null };
  if (DISCUSSION_CATEGORY_BADGE_RE.test(text)) return { kind: "discussion", discussionSection: null };
  return { kind: null, discussionSection: null };
}

/** Parse the rec-date start-time tail ("• 4:29 PM") as HH:MM 24h. */
export function parseRecordingTime(recDateText: string): string | null {
  const match = REC_TIME_RE.exec(recDateText);
  if (!match) return null;
  const hours = Number(match[1]);
  const minutes = Number(match[2]);
  if (!Number.isInteger(hours) || hours < 1 || hours > 12) return null;
  if (!Number.isInteger(minutes) || minutes < 0 || minutes > 59) return null;
  const isPm = match[3].toLowerCase() === "pm";
  const hour24 = (hours % 12) + (isPm ? 12 : 0);
  return `${String(hour24).padStart(2, "0")}:${String(minutes).padStart(2, "0")}`;
}

function compareCards(a: OverviewCardFact, b: OverviewCardFact): number {
  const aDate = a.lectureDate ?? "";
  const bDate = b.lectureDate ?? "";
  if (aDate !== bDate) return aDate < bDate ? -1 : 1;
  const aTime = a.recTime ?? "";
  const bTime = b.recTime ?? "";
  if (aTime !== bTime) return aTime < bTime ? -1 : 1;
  return a.href < b.href ? -1 : a.href > b.href ? 1 : 0;
}

export type DerivedIdentityResult =
  | { readonly ok: true; readonly kind: RecordingKind; readonly lectureNumber: number }
  | { readonly ok: false; readonly reason: string };

/**
 * Resolve a lagging unnumbered title from the overview sequence.
 *
 * Same-kind cards are ordered by rec-date then rec-time and walked with
 * next = 1: an explicit title number N requires N >= next and advances next to
 * N + 1; an unnumbered card takes next and advances it by one. The target
 * card's assigned number is the identity. Lecture-kind derivation also
 * requires one shared start time.
 */
export function deriveIdentityFromOverview(
  cards: readonly OverviewCardFact[],
  targetHref: string,
  targetTitle: string,
): DerivedIdentityResult {
  const titleIdentity = parseTitleIdentity(targetTitle);
  if (!titleIdentity || titleIdentity.lectureNumber !== null) {
    return {
      ok: false,
      reason: "the target recording title does not need overview derivation",
    };
  }

  const targets = cards.filter((card) => card.href === targetHref);
  if (targets.length !== 1) {
    return {
      ok: false,
      reason: `the linked overview has ${targets.length} player-link matches for the current recording; exactly one is required`,
    };
  }
  const target = targets[0];
  const kind = target.kind;
  if (kind === null) {
    return {
      ok: false,
      reason: "the correlated overview badge is not a lecture or discussion category",
    };
  }
  if (kind !== titleIdentity.kind) {
    return {
      ok: false,
      reason: "the recording title and overview badge category disagree",
    };
  }

  const sequence = cards.filter((card) => card.kind === kind).sort(compareCards);
  let next = 1;
  let assigned: number | null = null;
  const sequenceTimes = new Set<string>();

  for (const card of sequence) {
    const isTarget = card.href === targetHref;
    if (card.lectureDate === null) {
      return {
        ok: false,
        reason: "the same-kind overview sequence has an unparseable recording date",
      };
    }
    const identity = parseTitleIdentity(card.title);
    if (!identity) {
      return {
        ok: false,
        reason: "the same-kind overview sequence has an unrecognized recording title",
      };
    }
    if (identity.kind !== kind) {
      return {
        ok: false,
        reason: "the same-kind overview sequence has a recording title of another kind",
      };
    }
    if (identity.lectureNumber !== null) {
      if (identity.lectureNumber < next) {
        return {
          ok: false,
          reason: "the same-kind overview sequence contradicts its explicit lecture numbers",
        };
      }
      if (isTarget) {
        return {
          ok: false,
          reason: "the target recording title does not need overview derivation",
        };
      }
      next = identity.lectureNumber + 1;
    } else {
      if (isTarget) {
        assigned = next;
      }
      next += 1;
    }
    if (kind === "lecture") {
      if (card.recTime === null) {
        return {
          ok: false,
          reason: "the lecture-kind overview sequence is missing a start time",
        };
      }
      sequenceTimes.add(card.recTime);
    }
  }

  if (assigned === null) {
    return {
      ok: false,
      reason: "the target recording is not an unnumbered card in its overview sequence",
    };
  }
  if (kind === "lecture" && sequenceTimes.size !== 1) {
    return {
      ok: false,
      reason: "the lecture-kind overview cards do not share one start time",
    };
  }
  return { ok: true, kind, lectureNumber: assigned };
}
