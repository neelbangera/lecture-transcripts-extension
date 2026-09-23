/**
 * Persisted extension behavior settings.
 *
 * The options page writes two independent booleans to `chrome.storage.local`:
 * `autoCapture` controls whether the content runtime may open and capture a
 * transcript without a user click, and `notificationsEnabled` controls whether
 * the service worker may raise desktop notifications.  Both default to true
 * when storage is unavailable, unset, unreadable, or holds a non-boolean
 * value, so a corrupt setting can never silently disable a capture path.
 */

export const AUTO_CAPTURE_STORAGE_KEY = "autoCapture";
export const NOTIFICATIONS_ENABLED_STORAGE_KEY = "notificationsEnabled";

export interface ExtensionSettings {
  autoCapture: boolean;
  notificationsEnabled: boolean;
}

export const DEFAULT_EXTENSION_SETTINGS: Readonly<ExtensionSettings> =
  Object.freeze({
    autoCapture: true,
    notificationsEnabled: true,
  });

export interface SettingsStorageArea {
  get(keys?: string | string[] | null): Promise<Record<string, unknown>>;
  set(items: Record<string, unknown>): Promise<void>;
}

export class SettingsValidationError extends Error {
  readonly field: string;

  constructor(field: string) {
    super(`${field} must be a boolean`);
    this.name = "SettingsValidationError";
    this.field = field;
  }
}

export function defaultSettingsStorageArea(): SettingsStorageArea | null {
  const chromeApi = (
    globalThis as { chrome?: { storage?: { local?: SettingsStorageArea } } }
  ).chrome;
  return chromeApi?.storage?.local ?? null;
}

/** Pure: keep a stored boolean, otherwise fall back to the given default. */
export function booleanSetting(value: unknown, fallback: boolean): boolean {
  return typeof value === "boolean" ? value : fallback;
}

/** Pure: coerce an untrusted stored record into complete settings. */
export function validateSettings(value: unknown): ExtensionSettings {
  const record =
    typeof value === "object" && value !== null && !Array.isArray(value)
      ? (value as Record<string, unknown>)
      : {};
  return {
    autoCapture: booleanSetting(
      record[AUTO_CAPTURE_STORAGE_KEY],
      DEFAULT_EXTENSION_SETTINGS.autoCapture,
    ),
    notificationsEnabled: booleanSetting(
      record[NOTIFICATIONS_ENABLED_STORAGE_KEY],
      DEFAULT_EXTENSION_SETTINGS.notificationsEnabled,
    ),
  };
}

/**
 * Read both settings.  Each field falls back to its default independently when
 * storage is unavailable, unreadable, unset, or holds a non-boolean value.
 */
export async function loadSettings(
  area?: SettingsStorageArea | null,
): Promise<ExtensionSettings> {
  const target = area === undefined ? defaultSettingsStorageArea() : area;
  if (!target) return { ...DEFAULT_EXTENSION_SETTINGS };

  let result: Record<string, unknown>;
  try {
    result = await target.get([
      AUTO_CAPTURE_STORAGE_KEY,
      NOTIFICATIONS_ENABLED_STORAGE_KEY,
    ]);
  } catch {
    return { ...DEFAULT_EXTENSION_SETTINGS };
  }
  return validateSettings(result);
}

export async function loadAutoCapture(
  area?: SettingsStorageArea | null,
): Promise<boolean> {
  return (await loadSettings(area)).autoCapture;
}

export async function loadNotificationsEnabled(
  area?: SettingsStorageArea | null,
): Promise<boolean> {
  return (await loadSettings(area)).notificationsEnabled;
}

/** Validate, then persist both settings.  Non-boolean values are never written. */
export async function saveSettings(
  settings: unknown,
  area?: SettingsStorageArea | null,
): Promise<ExtensionSettings> {
  const record =
    typeof settings === "object" && settings !== null && !Array.isArray(settings)
      ? (settings as Record<string, unknown>)
      : {};
  for (const key of [
    AUTO_CAPTURE_STORAGE_KEY,
    NOTIFICATIONS_ENABLED_STORAGE_KEY,
  ]) {
    if (typeof record[key] !== "boolean") throw new SettingsValidationError(key);
  }

  const validated: ExtensionSettings = {
    autoCapture: record[AUTO_CAPTURE_STORAGE_KEY] as boolean,
    notificationsEnabled: record[NOTIFICATIONS_ENABLED_STORAGE_KEY] as boolean,
  };
  const target = area === undefined ? defaultSettingsStorageArea() : area;
  if (!target) throw new Error("chrome.storage.local is unavailable");
  await target.set({ ...validated });
  return validated;
}
