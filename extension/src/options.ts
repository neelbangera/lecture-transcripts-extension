/**
 * Options page: edit the persisted course allowlist and behavior settings.
 *
 * Every save is validated with the same rules the content runtime applies, so
 * an invalid allowlist can never reach `chrome.storage.local`.  The page is
 * seeded from the stored mappings, or from the built-in Stage 0 allowlist
 * when storage is empty or invalid.  The two behavior toggles persist
 * immediately on change and show their stored values on load.
 */

import type { CourseConfig } from "./course-config";
import {
  loadCourseMappings,
  saveCourseMappings,
  validateCourseMappings,
  type CourseMappingIssue,
} from "./course-storage";
import { loadSettings, saveSettings } from "./settings-storage";

function element<T extends HTMLElement>(id: string): T {
  const value = document.getElementById(id);
  if (!value) throw new Error(`missing options element: ${id}`);
  return value as T;
}

const statusLine = element<HTMLParagraphElement>("options-status");
const errorList = element<HTMLUListElement>("options-errors");
const courseList = element<HTMLElement>("course-list");
const addButton = element<HTMLButtonElement>("add-course");
const saveButton = element<HTMLButtonElement>("save-courses");
const reloadButton = element<HTMLButtonElement>("reload-courses");
const autoCaptureToggle = element<HTMLInputElement>("auto-capture-toggle");
const notificationsToggle = element<HTMLInputElement>("notifications-toggle");
const settingsStatus = element<HTMLParagraphElement>("settings-status");

const SEASONS = ["winter", "spring", "summer", "fall"] as const;
const SUCCESS_MESSAGE_MS = 3000;

let dirty = false;
let busy = false;
let statusTimer: number | null = null;

function termOptions(existing: readonly string[] = []): string[] {
  const currentYear = new Date().getFullYear();
  const options: string[] = [];
  for (let year = currentYear - 1; year <= currentYear + 2; year += 1) {
    for (const season of SEASONS) {
      options.push(`${year}-${season}`);
    }
  }
  for (const term of existing) {
    if (!options.includes(term)) options.push(term);
  }
  return options.sort();
}

const COURSE_TEMPLATE = `
  <legend>Course</legend>
  <div class="field-grid">
    <label class="field">Page course text
      <input data-field="pageCourseText" type="text" autocomplete="off" spellcheck="false" placeholder="EECS 484" />
    </label>
    <label class="field">Course name
      <input data-field="courseName" type="text" autocomplete="off" spellcheck="false" placeholder="EECS 484" />
    </label>
    <label class="field">Course slug
      <input data-field="courseSlug" type="text" autocomplete="off" spellcheck="false" placeholder="eecs484" />
    </label>
    <label class="field">Preferred discussion section (optional)
      <input data-field="preferredDiscussionSection" type="text" inputmode="numeric" autocomplete="off" spellcheck="false" placeholder="012" />
    </label>
  </div>
  <div class="field field-terms">
    <span class="field-label">Supported terms</span>
    <div class="term-row">
      <select data-term-select aria-label="Choose a supported term"></select>
      <button class="button secondary add-term" type="button">Add term</button>
    </div>
    <ul class="term-chips" data-term-chips aria-label="Selected terms"></ul>
  </div>
  <button class="button secondary remove-course" type="button">Remove course</button>
`;

function termChips(card: ParentNode): HTMLUListElement | null {
  return card.querySelector<HTMLUListElement>("[data-term-chips]");
}

function selectedTerms(card: ParentNode): string[] {
  const chips = termChips(card);
  if (!chips) return [];
  return Array.from(chips.querySelectorAll<HTMLLIElement>("[data-term]"))
    .map((chip) => chip.dataset.term ?? "")
    .filter(Boolean);
}

function renderTerms(card: ParentNode, terms: readonly string[]): void {
  const chips = termChips(card);
  if (!chips) return;
  chips.replaceChildren();
  for (const term of terms) {
    const chip = document.createElement("li");
    chip.className = "term-chip";
    chip.dataset.term = term;
    chip.append(document.createTextNode(term));

    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "chip-remove";
    remove.setAttribute("aria-label", `Remove term ${term}`);
    remove.textContent = "×";
    remove.addEventListener("click", () => {
      chip.remove();
      markDirty();
    });
    chip.append(remove);
    chips.append(chip);
  }
}

function addTerm(card: ParentNode): void {
  const select = card.querySelector<HTMLSelectElement>("[data-term-select]");
  const term = select?.value ?? "";
  if (!term || selectedTerms(card).includes(term)) return;
  renderTerms(card, [...selectedTerms(card), term].sort());
  markDirty();
}

function createCourseCard(course?: CourseConfig): HTMLFieldSetElement {
  const card = document.createElement("fieldset");
  card.className = "course card";
  card.dataset.course = "";
  card.innerHTML = COURSE_TEMPLATE;

  const select = card.querySelector<HTMLSelectElement>("[data-term-select]");
  if (select) {
    select.replaceChildren(
      ...termOptions(course?.supportedTerms).map((term) => {
        const option = document.createElement("option");
        option.value = term;
        option.textContent = term;
        return option;
      }),
    );
  }
  renderTerms(card, course?.supportedTerms ?? []);

  if (course) {
    setFieldValue(card, "pageCourseText", course.pageCourseText);
    setFieldValue(card, "courseName", course.courseName);
    setFieldValue(card, "courseSlug", course.courseSlug);
    setFieldValue(
      card,
      "preferredDiscussionSection",
      course.preferredDiscussionSection ?? "",
    );
  }

  card.querySelector(".add-term")?.addEventListener("click", () => {
    addTerm(card);
  });
  card.querySelector(".remove-course")?.addEventListener("click", () => {
    card.remove();
    markDirty();
  });
  card.addEventListener("input", () => markDirty());
  return card;
}

function fieldInput(
  card: ParentNode,
  field: string,
): HTMLInputElement | null {
  return card.querySelector<HTMLInputElement>(`[data-field="${field}"]`);
}

function setFieldValue(card: ParentNode, field: string, value: string): void {
  const input = fieldInput(card, field);
  if (input) input.value = value;
}

function renderCourses(courses: readonly CourseConfig[]): void {
  courseList.replaceChildren(...courses.map((course) => createCourseCard(course)));
}

function readCourses(): unknown[] {
  return Array.from(
    courseList.querySelectorAll<HTMLFieldSetElement>("fieldset[data-course]"),
  ).map((card) => ({
    pageCourseText: fieldInput(card, "pageCourseText")?.value ?? "",
    courseName: fieldInput(card, "courseName")?.value ?? "",
    courseSlug: fieldInput(card, "courseSlug")?.value ?? "",
    supportedTerms: selectedTerms(card),
    preferredDiscussionSection:
      (fieldInput(card, "preferredDiscussionSection")?.value ?? "").trim() || null,
  }));
}

type StatusVariant = "info" | "success" | "warn" | "error";

function setStatus(message: string, variant: StatusVariant = "info"): void {
  if (statusTimer !== null) {
    window.clearTimeout(statusTimer);
    statusTimer = null;
  }
  statusLine.textContent = message;
  statusLine.classList.remove("notice-success", "notice-warn", "notice-error");
  if (variant !== "info") statusLine.classList.add(`notice-${variant}`);
  if (variant === "success") {
    statusTimer = window.setTimeout(() => {
      statusTimer = null;
      statusLine.textContent = "";
      statusLine.classList.remove("notice-success");
    }, SUCCESS_MESSAGE_MS);
  }
}

function refreshSaveState(): void {
  saveButton.disabled = busy || !dirty;
}

function markDirty(): void {
  dirty = true;
  refreshSaveState();
  setStatus("Unsaved changes. Select Save to persist them.", "warn");
}

function setBusy(value: boolean): void {
  busy = value;
  addButton.disabled = value;
  reloadButton.disabled = value;
  refreshSaveState();
}

function renderIssues(issues: readonly CourseMappingIssue[]): void {
  errorList.replaceChildren();
  errorList.classList.toggle("hidden", issues.length === 0);

  for (const card of courseList.querySelectorAll<HTMLFieldSetElement>(
    "fieldset[data-course]",
  )) {
    for (const input of card.querySelectorAll<HTMLInputElement>("[data-field]")) {
      input.removeAttribute("aria-invalid");
    }
  }

  for (const issue of issues) {
    const item = document.createElement("li");
    item.textContent = issue.message;
    errorList.append(item);

    if (issue.index !== null && issue.field !== null) {
      const card = courseList.querySelectorAll<HTMLFieldSetElement>(
        "fieldset[data-course]",
      )[issue.index];
      const input = card ? fieldInput(card, issue.field) : null;
      input?.setAttribute("aria-invalid", "true");
    }
  }
}

async function save(): Promise<void> {
  setBusy(true);
  try {
    const validation = validateCourseMappings(readCourses());
    if (!validation.ok) {
      renderIssues(validation.issues);
      setStatus("Not saved: fix the listed problems first.", "error");
      return;
    }

    const saved = await saveCourseMappings(validation.mappings);
    renderIssues([]);
    dirty = false;
    setStatus(
      saved.length === 1 ? "Saved ✓ (1 course)" : `Saved ✓ (${saved.length} courses)`,
      "success",
    );
  } catch (error) {
    renderIssues([]);
    setStatus(
      error instanceof Error
        ? `Not saved: ${error.message}`
        : "Not saved: storage is unavailable.",
      "error",
    );
  } finally {
    setBusy(false);
    refreshSaveState();
  }
}

async function reload(): Promise<void> {
  setBusy(true);
  try {
    const courses = await loadCourseMappings();
    renderCourses(courses);
    renderIssues([]);
    dirty = false;
    setStatus(
      courses.length === 1
        ? "Loaded 1 course mapping."
        : `Loaded ${courses.length} course mappings.`,
    );
  } finally {
    setBusy(false);
    refreshSaveState();
  }
}

function readSettings(): { autoCapture: boolean; notificationsEnabled: boolean } {
  return {
    autoCapture: autoCaptureToggle.checked,
    notificationsEnabled: notificationsToggle.checked,
  };
}

function applySettings(settings: {
  autoCapture: boolean;
  notificationsEnabled: boolean;
}): void {
  autoCaptureToggle.checked = settings.autoCapture;
  notificationsToggle.checked = settings.notificationsEnabled;
}

let settingsStatusTimer: number | null = null;

function setSettingsStatus(message: string, success = false): void {
  if (settingsStatusTimer !== null) {
    window.clearTimeout(settingsStatusTimer);
    settingsStatusTimer = null;
  }
  settingsStatus.textContent = message;
  settingsStatus.classList.toggle("notice-success", success);
  settingsStatus.classList.toggle("notice-error", !success && message !== "");
  if (success) {
    settingsStatusTimer = window.setTimeout(() => {
      settingsStatusTimer = null;
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("notice-success");
    }, SUCCESS_MESSAGE_MS);
  }
}

async function loadSettingsToggles(): Promise<void> {
  applySettings(await loadSettings());
}

async function saveSettingsToggles(): Promise<void> {
  const desired = readSettings();
  autoCaptureToggle.disabled = true;
  notificationsToggle.disabled = true;
  try {
    await saveSettings(desired);
    setSettingsStatus("Saved ✓", true);
  } catch (error) {
    setSettingsStatus(
      error instanceof Error
        ? `Not saved: ${error.message}`
        : "Not saved: storage is unavailable.",
    );
    await loadSettingsToggles();
  } finally {
    autoCaptureToggle.disabled = false;
    notificationsToggle.disabled = false;
  }
}

addButton.addEventListener("click", () => {
  courseList.append(createCourseCard());
  markDirty();
});
saveButton.addEventListener("click", () => void save());
reloadButton.addEventListener("click", () => void reload());
autoCaptureToggle.addEventListener("change", () => void saveSettingsToggles());
notificationsToggle.addEventListener("change", () => void saveSettingsToggles());

void reload();
void loadSettingsToggles();
