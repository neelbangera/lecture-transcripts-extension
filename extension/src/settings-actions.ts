/**
 * DOM-free helpers for the settings entry points.
 *
 * The popup shows a Settings button only when the runtime exposes
 * `chrome.runtime.openOptionsPage`.  These helpers keep that decision and the
 * click handling testable without a popup document.
 */

export interface OptionsPageRuntime {
  openOptionsPage?(callback?: () => void): void | Promise<void>;
}

export function canOpenOptionsPage(
  runtime: OptionsPageRuntime | undefined | null,
): boolean {
  return typeof runtime?.openOptionsPage === "function";
}

/** Open the options page; returns false when the API is missing or throws. */
export function openOptionsPage(
  runtime: OptionsPageRuntime | undefined | null,
): boolean {
  if (!runtime || typeof runtime.openOptionsPage !== "function") return false;
  try {
    void runtime.openOptionsPage();
    return true;
  } catch {
    return false;
  }
}
