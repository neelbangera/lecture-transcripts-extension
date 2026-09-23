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
    <label class="field">Supported terms (comma-separated)
      <input data-field="supportedTerms" type="text" autocomplete="off" spellcheck="false" placeholder="2026-fall, 2027-winter" />
    </label>
    <label class="field">Preferred discussion section (optional)
      <input data-field="preferredDiscussionSection" type="text" inputmode="numeric" autocomplete="off" spellcheck="false" placeholder="012" />
    </label>
  </div>
  <button class="button secondary remove-course" type="button">Remove course</button>
`;

function createCourseCard(course?: CourseConfig): HTMLFieldSetElement {
  const card = document.createElement("fieldset");
  card.className = "course card";
  card.dataset.course = "";
  card.innerHTML = COURSE_TEMPLATE;

  if (course) {
    setFieldValue(card, "pageCourseText", course.pageCourseText);
    setFieldValue(card, "courseName", course.courseName);
    setFieldValue(card, "courseSlug", course.courseSlug);
    setFieldValue(card, "supportedTerms", course.supportedTerms.join(", "));
    setFieldValue(
      card,
      "preferredDiscussionSection",
      course.preferredDiscussionSection ?? "",
    );
  }

  card.querySelector(".remove-course")?.addEventListener("click", () => {
    card.remove();
    setStatus("Unsaved changes. Select Save to persist them.");
  });
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
    supportedTerms: (fieldInput(card, "supportedTerms")?.value ?? "")
      .split(/[,\s]+/u)
      .filter(Boolean),
    preferredDiscussionSection:
      (fieldInput(card, "preferredDiscussionSection")?.value ?? "").trim() || null,
  }));
}

function setStatus(message: string): void {
  statusLine.textContent = message;
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

function setBusy(value: boolean): void {
  addButton.disabled = value;
  saveButton.disabled = value;
  reloadButton.disabled = value;
}

async function save(): Promise<void> {
  setBusy(true);
  try {
    const validation = validateCourseMappings(readCourses());
    if (!validation.ok) {
      renderIssues(validation.issues);
      setStatus("Not saved: fix the listed problems first.");
      return;
    }

    const saved = await saveCourseMappings(validation.mappings);
    renderIssues([]);
    setStatus(
      saved.length === 1
        ? "Saved 1 course mapping."
        : `Saved ${saved.length} course mappings.`,
    );
  } catch (error) {
    renderIssues([]);
    setStatus(
      error instanceof Error
        ? `Not saved: ${error.message}`
        : "Not saved: storage is unavailable.",
    );
  } finally {
    setBusy(false);
  }
}

async function reload(): Promise<void> {
  setBusy(true);
  try {
    const courses = await loadCourseMappings();
    renderCourses(courses);
    renderIssues([]);
    setStatus(
      courses.length === 1
        ? "Loaded 1 course mapping."
        : `Loaded ${courses.length} course mappings.`,
    );
  } finally {
    setBusy(false);
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

function setSettingsStatus(message: string): void {
  settingsStatus.textContent = message;
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
    setSettingsStatus("Saved settings.");
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
  setStatus("Unsaved changes. Select Save to persist them.");
});
saveButton.addEventListener("click", () => void save());
reloadButton.addEventListener("click", () => void reload());
autoCaptureToggle.addEventListener("change", () => void saveSettingsToggles());
notificationsToggle.addEventListener("change", () => void saveSettingsToggles());

void reload();
void loadSettingsToggles();
