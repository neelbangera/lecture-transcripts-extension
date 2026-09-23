/**
 * Options page dirty-state and save/reload interaction tests.
 *
 * The options module wires itself up on import, so each test mounts a minimal
 * DOM matching `options.html`, installs an in-memory `chrome.storage.local`,
 * then dynamically imports the module with a fresh module registry.
 */

import { JSDOM } from "jsdom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { COURSE_MAPPINGS_STORAGE_KEY } from "../extension/src/course-storage";
import {
  AUTO_CAPTURE_STORAGE_KEY,
  NOTIFICATIONS_ENABLED_STORAGE_KEY,
} from "../extension/src/settings-storage";

const OPTIONS_MARKUP = `<!doctype html><html><body>
  <p id="options-status" class="notice" aria-live="polite"></p>
  <ul id="options-errors" class="error-list hidden" aria-live="assertive"></ul>
  <section id="course-list" class="course-list"></section>
  <p id="course-list-empty" class="muted course-list-empty hidden" aria-live="polite"></p>
  <button id="add-course" type="button"></button>
  <button id="reload-courses" type="button"></button>
  <button id="save-courses" type="button" disabled></button>
  <input id="auto-capture-toggle" type="checkbox" />
  <input id="notifications-toggle" type="checkbox" />
  <p id="settings-status" class="notice" aria-live="polite"></p>
</body></html>`;

const STORED_COURSE = {
  pageCourseText: "EECS 484",
  courseName: "EECS 484",
  courseSlug: "eecs484",
  supportedTerms: ["2026-fall"],
  preferredDiscussionSection: null,
};

class MemoryStorage {
  readonly values = new Map<string, unknown>();
  held = false;
  private pending: Array<() => void> = [];

  async get(keys?: string | string[] | null): Promise<Record<string, unknown>> {
    if (keys === undefined || keys === null) {
      return structuredClone(Object.fromEntries(this.values));
    }
    const list = Array.isArray(keys) ? keys : [keys];
    const result: Record<string, unknown> = {};
    for (const key of list) {
      if (this.values.has(key)) result[key] = structuredClone(this.values.get(key));
    }
    return result;
  }

  async set(items: Record<string, unknown>): Promise<void> {
    if (this.held) {
      await new Promise<void>((resolve) => this.pending.push(resolve));
    }
    for (const [key, value] of Object.entries(items)) {
      this.values.set(key, structuredClone(value));
    }
  }

  release(): void {
    this.held = false;
    const waiting = this.pending;
    this.pending = [];
    for (const resolve of waiting) resolve();
  }
}

let dom: JSDOM;
let storage: MemoryStorage;

function element<T extends HTMLElement>(id: string): T {
  const found = dom.window.document.getElementById(id);
  if (!found) throw new Error(`missing fixture element: ${id}`);
  return found as unknown as T;
}

function courseCards(): HTMLFieldSetElement[] {
  return Array.from(
    dom.window.document.querySelectorAll<HTMLFieldSetElement>("fieldset[data-course]"),
  );
}

function field(card: ParentNode, name: string): HTMLInputElement {
  const input = card.querySelector<HTMLInputElement>(`[data-field="${name}"]`);
  if (!input) throw new Error(`missing fixture field: ${name}`);
  return input;
}

function termSelect(card: ParentNode): HTMLSelectElement {
  const select = card.querySelector<HTMLSelectElement>("[data-term-select]");
  if (!select) throw new Error("missing fixture term select");
  return select;
}

function termChips(card: ParentNode): HTMLLIElement[] {
  return Array.from(card.querySelectorAll<HTMLLIElement>("[data-term]"));
}

function saveButton(): HTMLButtonElement {
  return element<HTMLButtonElement>("save-courses");
}

function statusText(): string {
  return element("options-status").textContent ?? "";
}

function fire(target: EventTarget, type: string): void {
  target.dispatchEvent(new dom.window.Event(type, { bubbles: true }));
}

async function settle(rounds = 8): Promise<void> {
  for (let round = 0; round < rounds; round += 1) {
    await new Promise<void>((resolve) => setTimeout(resolve, 0));
  }
}

async function mountOptionsPage(): Promise<void> {
  dom = new JSDOM(OPTIONS_MARKUP);
  storage = new MemoryStorage();
  storage.values.set(COURSE_MAPPINGS_STORAGE_KEY, [structuredClone(STORED_COURSE)]);
  const globals = globalThis as Record<string, unknown>;
  globals.window = dom.window;
  globals.document = dom.window.document;
  globals.chrome = { storage: { local: storage } };
  vi.resetModules();
  await import("../extension/src/options");
  await settle();
}

afterEach(() => {
  const globals = globalThis as Record<string, unknown>;
  delete globals.window;
  delete globals.document;
  delete globals.chrome;
  vi.resetModules();
});

describe("options page dirty state", () => {
  it("starts clean with Save disabled after loading stored courses", async () => {
    await mountOptionsPage();
    expect(courseCards()).toHaveLength(1);
    expect(saveButton().disabled).toBe(true);
    expect(statusText()).toBe("Loaded 1 course mapping.");
  });

  it("focusing a field does not mark the form dirty", async () => {
    await mountOptionsPage();
    const input = field(courseCards()[0], "pageCourseText");
    input.focus();
    fire(input, "focus");
    expect(saveButton().disabled).toBe(true);
    expect(statusText()).not.toContain("Unsaved changes");
  });

  it("highlighting a term dropdown option does not mark the form dirty", async () => {
    await mountOptionsPage();
    const select = termSelect(courseCards()[0]);
    const choice = Array.from(select.options).find(
      (option) => option.value !== "" && option.value !== "2026-fall",
    );
    expect(choice).toBeDefined();
    select.value = choice?.value ?? "";
    fire(select, "input");
    fire(select, "change");
    expect(saveButton().disabled).toBe(true);
    expect(statusText()).toBe("Loaded 1 course mapping.");
    expect(termChips(courseCards()[0])).toHaveLength(1);
  });

  it("editing a text field marks the form dirty", async () => {
    await mountOptionsPage();
    const input = field(courseCards()[0], "pageCourseText");
    input.value = "EECS 999";
    fire(input, "input");
    expect(saveButton().disabled).toBe(false);
    expect(statusText()).toContain("Unsaved changes");
  });

  it("adding and removing a term chip marks the form dirty", async () => {
    await mountOptionsPage();
    const card = courseCards()[0];
    const select = termSelect(card);
    const choice = Array.from(select.options).find(
      (option) => option.value !== "" && option.value !== "2026-fall",
    );
    const term = choice?.value ?? "";
    select.value = term;
    card.querySelector<HTMLButtonElement>(".add-term")?.click();
    expect(termChips(card).map((chip) => chip.dataset.term)).toContain(term);
    expect(saveButton().disabled).toBe(false);
    expect(statusText()).toContain("Unsaved changes");

    const added = termChips(card).find((chip) => chip.dataset.term === term);
    added?.querySelector<HTMLButtonElement>(".chip-remove")?.click();
    expect(termChips(card).map((chip) => chip.dataset.term)).not.toContain(term);
    expect(saveButton().disabled).toBe(false);
  });

  it("adding and removing a course marks the form dirty", async () => {
    await mountOptionsPage();
    element<HTMLButtonElement>("add-course").click();
    expect(courseCards()).toHaveLength(2);
    expect(saveButton().disabled).toBe(false);
    expect(element("course-list-empty").classList.contains("hidden")).toBe(true);

    courseCards()[1]
      .querySelector<HTMLButtonElement>(".remove-course")
      ?.click();
    expect(courseCards()).toHaveLength(1);
    expect(saveButton().disabled).toBe(false);
  });

  it("shows the empty state when the last course is removed", async () => {
    await mountOptionsPage();
    courseCards()[0]
      .querySelector<HTMLButtonElement>(".remove-course")
      ?.click();
    expect(courseCards()).toHaveLength(0);
    expect(element("course-list-empty").classList.contains("hidden")).toBe(false);
    element<HTMLButtonElement>("add-course").click();
    expect(element("course-list-empty").classList.contains("hidden")).toBe(true);
  });

  it("saving persists edits and clears the dirty state", async () => {
    await mountOptionsPage();
    const input = field(courseCards()[0], "pageCourseText");
    input.value = "EECS 999";
    fire(input, "input");
    saveButton().click();
    await settle();
    expect(saveButton().disabled).toBe(true);
    expect(statusText()).toContain("Saved ✓");
    const stored = storage.values.get(COURSE_MAPPINGS_STORAGE_KEY) as Array<{
      pageCourseText: string;
    }>;
    expect(stored[0]?.pageCourseText).toBe("EECS 999");
  });

  it("reloading restores stored values and clears the dirty state", async () => {
    await mountOptionsPage();
    const input = field(courseCards()[0], "pageCourseText");
    input.value = "EECS 999";
    fire(input, "input");
    expect(saveButton().disabled).toBe(false);
    element<HTMLButtonElement>("reload-courses").click();
    await settle();
    expect(saveButton().disabled).toBe(true);
    expect(field(courseCards()[0], "pageCourseText").value).toBe("EECS 484");
    expect(statusText()).toContain("Loaded 1 course mapping.");
  });

  it("locks card controls while a save is in flight", async () => {
    await mountOptionsPage();
    const card = courseCards()[0];
    const input = field(card, "pageCourseText");
    input.value = "EECS 999";
    fire(input, "input");
    storage.held = true;
    saveButton().click();
    await settle(2);
    expect(input.disabled).toBe(true);
    expect(
      card.querySelector<HTMLButtonElement>(".remove-course")?.disabled,
    ).toBe(true);
    expect(
      card.querySelector<HTMLButtonElement>(".add-term")?.disabled,
    ).toBe(true);
    storage.release();
    await settle();
    expect(input.disabled).toBe(false);
    expect(saveButton().disabled).toBe(true);
  });

  it("toggling a behavior setting saves immediately without dirtying the form", async () => {
    await mountOptionsPage();
    const toggle = element<HTMLInputElement>("auto-capture-toggle");
    expect(toggle.checked).toBe(true);
    toggle.checked = false;
    fire(toggle, "change");
    await settle();
    expect(saveButton().disabled).toBe(true);
    expect(statusText()).not.toContain("Unsaved changes");
    expect(storage.values.get(AUTO_CAPTURE_STORAGE_KEY)).toBe(false);
    expect(storage.values.get(NOTIFICATIONS_ENABLED_STORAGE_KEY)).toBe(true);
    expect(element("settings-status").textContent).toContain("Saved ✓");
  });

  it("blocks invalid saves, marks the field, and keeps the form dirty", async () => {
    await mountOptionsPage();
    const input = field(courseCards()[0], "pageCourseText");
    input.value = "";
    fire(input, "input");
    saveButton().click();
    await settle();
    expect(statusText()).toContain("Not saved");
    expect(saveButton().disabled).toBe(false);
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(element("options-errors").classList.contains("hidden")).toBe(false);
    expect(storage.values.get(COURSE_MAPPINGS_STORAGE_KEY)).toEqual([
      STORED_COURSE,
    ]);
  });

  it("marks the term select when supported terms are missing", async () => {
    await mountOptionsPage();
    element<HTMLButtonElement>("add-course").click();
    saveButton().click();
    await settle();
    const added = courseCards()[1];
    expect(
      added.querySelector("[data-term-select]")?.getAttribute("aria-invalid"),
    ).toBe("true");
    expect(
      field(added, "pageCourseText").getAttribute("aria-invalid"),
    ).toBe("true");
    expect(
      courseCards()[0]
        .querySelector("[data-term-select]")
        ?.getAttribute("aria-invalid"),
    ).toBeNull();
  });
});
