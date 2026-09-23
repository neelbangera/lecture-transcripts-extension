import { afterEach, describe, expect, it } from "vitest";

import {
  AUTO_CAPTURE_STORAGE_KEY,
  DEFAULT_EXTENSION_SETTINGS,
  NOTIFICATIONS_ENABLED_STORAGE_KEY,
  SettingsValidationError,
  booleanSetting,
  loadAutoCapture,
  loadNotificationsEnabled,
  loadSettings,
  saveSettings,
  validateSettings,
  type SettingsStorageArea,
} from "../extension/src/settings-storage";

class MemoryStorageArea implements SettingsStorageArea {
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

function setChromeStorage(area: SettingsStorageArea | null): void {
  (globalThis as Record<string, unknown>).chrome = area
    ? { storage: { local: area } }
    : {};
}

afterEach(() => {
  delete (globalThis as Record<string, unknown>).chrome;
});

describe("extension settings validation", () => {
  it("defaults both settings to true for unset or malformed records", () => {
    expect(DEFAULT_EXTENSION_SETTINGS).toEqual({
      autoCapture: true,
      notificationsEnabled: true,
    });
    for (const value of [undefined, null, "settings", [], 7]) {
      expect(validateSettings(value)).toEqual(DEFAULT_EXTENSION_SETTINGS);
    }
  });

  it("keeps stored booleans and falls back per field for non-booleans", () => {
    expect(
      validateSettings({ autoCapture: false, notificationsEnabled: false }),
    ).toEqual({ autoCapture: false, notificationsEnabled: false });
    expect(
      validateSettings({ autoCapture: "false", notificationsEnabled: 0 }),
    ).toEqual(DEFAULT_EXTENSION_SETTINGS);
    expect(
      validateSettings({ autoCapture: false, notificationsEnabled: "yes" }),
    ).toEqual({ autoCapture: false, notificationsEnabled: true });
  });

  it("exposes the pure boolean fallback", () => {
    expect(booleanSetting(true, false)).toBe(true);
    expect(booleanSetting(false, true)).toBe(false);
    expect(booleanSetting("true", true)).toBe(true);
    expect(booleanSetting(1, false)).toBe(false);
  });
});

describe("extension settings storage", () => {
  it("loads defaults when storage is unavailable or empty", async () => {
    await expect(loadSettings(undefined)).resolves.toEqual(
      DEFAULT_EXTENSION_SETTINGS,
    );
    await expect(loadSettings(null)).resolves.toEqual(DEFAULT_EXTENSION_SETTINGS);
    await expect(loadSettings(new MemoryStorageArea())).resolves.toEqual(
      DEFAULT_EXTENSION_SETTINGS,
    );

    setChromeStorage(null);
    await expect(loadSettings()).resolves.toEqual(DEFAULT_EXTENSION_SETTINGS);
  });

  it("loads defaults when the storage read fails", async () => {
    const area = new MemoryStorageArea();
    area.failOnGet = true;
    await expect(loadSettings(area)).resolves.toEqual(DEFAULT_EXTENSION_SETTINGS);
  });

  it("reads stored booleans and rejects non-boolean values", async () => {
    const area = new MemoryStorageArea();
    area.values.set(AUTO_CAPTURE_STORAGE_KEY, false);
    area.values.set(NOTIFICATIONS_ENABLED_STORAGE_KEY, false);
    await expect(loadSettings(area)).resolves.toEqual({
      autoCapture: false,
      notificationsEnabled: false,
    });
    await expect(loadAutoCapture(area)).resolves.toBe(false);
    await expect(loadNotificationsEnabled(area)).resolves.toBe(false);

    area.values.set(AUTO_CAPTURE_STORAGE_KEY, "false");
    area.values.set(NOTIFICATIONS_ENABLED_STORAGE_KEY, 1);
    await expect(loadSettings(area)).resolves.toEqual(DEFAULT_EXTENSION_SETTINGS);
  });

  it("saves validated booleans under the settings keys and reloads them", async () => {
    const area = new MemoryStorageArea();
    const saved = await saveSettings(
      { autoCapture: false, notificationsEnabled: true },
      area,
    );

    expect(saved).toEqual({ autoCapture: false, notificationsEnabled: true });
    expect(area.values.get(AUTO_CAPTURE_STORAGE_KEY)).toBe(false);
    expect(area.values.get(NOTIFICATIONS_ENABLED_STORAGE_KEY)).toBe(true);
    await expect(loadSettings(area)).resolves.toEqual(saved);
  });

  it("never writes non-boolean settings", async () => {
    const area = new MemoryStorageArea();
    await expect(
      saveSettings({ autoCapture: "false", notificationsEnabled: true }, area),
    ).rejects.toBeInstanceOf(SettingsValidationError);
    await expect(
      saveSettings({ autoCapture: true }, area),
    ).rejects.toBeInstanceOf(SettingsValidationError);
    await expect(saveSettings(null, area)).rejects.toBeInstanceOf(
      SettingsValidationError,
    );
    expect(area.values.has(AUTO_CAPTURE_STORAGE_KEY)).toBe(false);
    expect(area.values.has(NOTIFICATIONS_ENABLED_STORAGE_KEY)).toBe(false);
  });

  it("uses chrome.storage.local through the default area", async () => {
    const area = new MemoryStorageArea();
    area.values.set(AUTO_CAPTURE_STORAGE_KEY, false);
    setChromeStorage(area);

    await expect(loadAutoCapture()).resolves.toBe(false);
    await expect(
      saveSettings({ autoCapture: true, notificationsEnabled: false }),
    ).resolves.toEqual({ autoCapture: true, notificationsEnabled: false });
    expect(area.values.get(AUTO_CAPTURE_STORAGE_KEY)).toBe(true);
    expect(area.values.get(NOTIFICATIONS_ENABLED_STORAGE_KEY)).toBe(false);
  });

  it("fails save when chrome.storage.local is unavailable", async () => {
    setChromeStorage(null);
    await expect(
      saveSettings({ autoCapture: true, notificationsEnabled: true }),
    ).rejects.toThrow("chrome.storage.local is unavailable");
  });
});
