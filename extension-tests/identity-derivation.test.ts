import { describe, expect, it } from "vitest";

import {
  deriveIdentityFromOverview,
  parseRecordingBadge,
  parseRecordingTime,
  parseTitleIdentity,
  type OverviewCardFact,
} from "../extension/src/identity-derivation";

function card(partial: Partial<OverviewCardFact> & { href: string; title: string }): OverviewCardFact {
  return {
    lectureDate: "2026-09-01",
    recTime: "16:30",
    kind: "lecture",
    discussionSection: null,
    ...partial,
  };
}

describe("parseTitleIdentity", () => {
  it("reads every explicit title shape", () => {
    expect(parseTitleIdentity("01 Intro, Smith")).toEqual({
      kind: "lecture",
      lectureNumber: 1,
      numberSource: "title",
    });
    expect(parseTitleIdentity("Lecture: 5")).toEqual({
      kind: "lecture",
      lectureNumber: 5,
      numberSource: "title",
    });
    expect(parseTitleIdentity("Lecture:5")).toMatchObject({ lectureNumber: 5 });
    expect(parseTitleIdentity("Lecture 12")).toMatchObject({
      kind: "lecture",
      lectureNumber: 12,
    });
    expect(parseTitleIdentity("Discussion 2, Smith")).toEqual({
      kind: "discussion",
      lectureNumber: 2,
      numberSource: "title",
    });
    expect(parseTitleIdentity("Discussion: 3")).toMatchObject({
      kind: "discussion",
      lectureNumber: 3,
    });
  });

  it("recognizes the lag form as needing derivation", () => {
    expect(parseTitleIdentity("Lecture recorded on 9/17/2026")).toEqual({
      kind: "lecture",
      lectureNumber: null,
      numberSource: "derived",
    });
    expect(parseTitleIdentity("Discussion recorded on 9/18/2026")).toEqual({
      kind: "discussion",
      lectureNumber: null,
      numberSource: "derived",
    });
  });

  it("rejects decoys and out-of-range numbers", () => {
    expect(parseTitleIdentity("DISREGARD -- Empty discussion")).toBeNull();
    expect(parseTitleIdentity("Welcome to the course")).toBeNull();
    expect(parseTitleIdentity("")).toBeNull();
    expect(parseTitleIdentity("Lecture - 001")).toBeNull();
    expect(parseTitleIdentity("1000 Intro")).toBeNull();
    expect(parseTitleIdentity("0 Intro")).toBeNull();
  });
});

describe("parseRecordingBadge", () => {
  it("reads the category and the discussion section without using the badge number", () => {
    expect(parseRecordingBadge("Lecture - 001")).toEqual({
      kind: "lecture",
      discussionSection: null,
    });
    expect(parseRecordingBadge("Discussion - 012")).toEqual({
      kind: "discussion",
      discussionSection: "012",
    });
    expect(parseRecordingBadge("Discussion - 12")).toEqual({
      kind: "discussion",
      discussionSection: null,
    });
    expect(parseRecordingBadge("Office hours")).toEqual({
      kind: null,
      discussionSection: null,
    });
  });
});

describe("parseRecordingTime", () => {
  it("parses the rec-date start-time tail as 24h HH:MM", () => {
    expect(parseRecordingTime("9/1/2026 \u2022 4:29 PM")).toBe("16:29");
    expect(parseRecordingTime("2/12/2027 \u2022 9:05 AM")).toBe("09:05");
    expect(parseRecordingTime("2/12/2027 \u2022 12:00 PM")).toBe("12:00");
    expect(parseRecordingTime("2/12/2027 \u2022 12:00 AM")).toBe("00:00");
    expect(parseRecordingTime("2/12/2027")).toBeNull();
    expect(parseRecordingTime("2/12/2027 \u2022 13:00 PM")).toBeNull();
  });
});

describe("deriveIdentityFromOverview", () => {
  const sequence = [
    card({ href: "https://leccap.engin.umich.edu/leccap/player/r/a", title: "01 Intro", lectureDate: "2026-09-01" }),
    card({ href: "https://leccap.engin.umich.edu/leccap/player/r/b", title: "02 ER Model", lectureDate: "2026-09-03" }),
    card({
      href: "https://leccap.engin.umich.edu/leccap/player/r/c",
      title: "Lecture recorded on 9/17/2026",
      lectureDate: "2026-09-17",
    }),
  ];

  it("assigns the next number after the last explicit one", () => {
    expect(
      deriveIdentityFromOverview(sequence, sequence[2].href, sequence[2].title),
    ).toEqual({ ok: true, kind: "lecture", lectureNumber: 3 });
  });

  it("numbers consecutive unnumbered cards in order", () => {
    const cards = [
      sequence[0],
      sequence[1],
      card({
        href: "https://leccap.engin.umich.edu/leccap/player/r/c",
        title: "Lecture recorded on 9/15/2026",
        lectureDate: "2026-09-15",
      }),
      card({
        href: "https://leccap.engin.umich.edu/leccap/player/r/d",
        title: "Lecture recorded on 9/17/2026",
        lectureDate: "2026-09-17",
      }),
    ];
    expect(
      deriveIdentityFromOverview(cards, cards[2].href, cards[2].title),
    ).toEqual({ ok: true, kind: "lecture", lectureNumber: 3 });
    expect(
      deriveIdentityFromOverview(cards, cards[3].href, cards[3].title),
    ).toEqual({ ok: true, kind: "lecture", lectureNumber: 4 });
  });

  it("keeps lecture and discussion sequences independent", () => {
    const cards = [
      ...sequence,
      card({
        href: "https://leccap.engin.umich.edu/leccap/player/r/d",
        title: "Discussion recorded on 9/8/2026",
        lectureDate: "2026-09-08",
        kind: "discussion",
        discussionSection: "012",
        recTime: "10:00",
      }),
    ];
    expect(
      deriveIdentityFromOverview(cards, cards[3].href, cards[3].title),
    ).toEqual({ ok: true, kind: "discussion", lectureNumber: 1 });
  });

  it("fails closed on contradictions, decoys, kind disagreement, and start-time drift", () => {
    // An unnumbered card between 01 and an explicit 02 has no valid slot.
    const contradicted = [
      sequence[0],
      card({
        href: "https://leccap.engin.umich.edu/leccap/player/r/mid",
        title: "Lecture recorded on 9/2/2026",
        lectureDate: "2026-09-02",
      }),
      sequence[1],
    ];
    expect(
      deriveIdentityFromOverview(contradicted, contradicted[1].href, contradicted[1].title),
    ).toMatchObject({ ok: false });

    // An explicit number below the running counter is a contradiction.
    const duplicate = [
      sequence[0],
      card({
        href: "https://leccap.engin.umich.edu/leccap/player/r/mid",
        title: "Lecture recorded on 9/2/2026",
        lectureDate: "2026-09-02",
      }),
      card({ href: "https://leccap.engin.umich.edu/leccap/player/r/late", title: "01 Again", lectureDate: "2026-09-08" }),
    ];
    expect(
      deriveIdentityFromOverview(duplicate, duplicate[1].href, duplicate[1].title),
    ).toMatchObject({ ok: false });

    const decoyNeighbor = [
      sequence[0],
      card({ href: "https://leccap.engin.umich.edu/leccap/player/r/x", title: "DISREGARD -- Empty discussion" }),
      sequence[2],
    ];
    expect(
      deriveIdentityFromOverview(decoyNeighbor, sequence[2].href, sequence[2].title),
    ).toMatchObject({ ok: false });

    const kindDisagreement = [
      sequence[0],
      sequence[1],
      card({
        href: "https://leccap.engin.umich.edu/leccap/player/r/c",
        title: "Lecture recorded on 9/17/2026",
        lectureDate: "2026-09-17",
        kind: "discussion",
      }),
    ];
    expect(
      deriveIdentityFromOverview(kindDisagreement, kindDisagreement[2].href, kindDisagreement[2].title),
    ).toMatchObject({ ok: false });

    const timeDrift = [
      sequence[0],
      card({ href: "https://leccap.engin.umich.edu/leccap/player/r/b", title: "02 ER Model", lectureDate: "2026-09-03", recTime: "17:00" }),
      sequence[2],
    ];
    expect(
      deriveIdentityFromOverview(timeDrift, sequence[2].href, sequence[2].title),
    ).toMatchObject({ ok: false });

    const missingTime = [
      sequence[0],
      sequence[1],
      card({
        href: "https://leccap.engin.umich.edu/leccap/player/r/c",
        title: "Lecture recorded on 9/17/2026",
        lectureDate: "2026-09-17",
        recTime: null,
      }),
    ];
    expect(
      deriveIdentityFromOverview(missingTime, missingTime[2].href, missingTime[2].title),
    ).toMatchObject({ ok: false });
  });

  it("refuses to derive for an explicit target title", () => {
    expect(
      deriveIdentityFromOverview(sequence, sequence[0].href, sequence[0].title),
    ).toMatchObject({ ok: false });
  });
});
