import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const repositoryRoot = fileURLToPath(new URL("../", import.meta.url));
const outputRoot = join(repositoryRoot, "dist", "extension");
const fixtureRoot = join(repositoryRoot, "extension-tests", "fixtures");

const unresolvedPlaceholderPattern = /(?<!<define:)__STAGE0_SELECTORS__/;

interface ExtensionManifest {
  background: { service_worker: string };
  action: {
    default_popup: string;
    default_icon?: string | Record<string, string>;
  };
  options_ui: { page: string; open_in_tab: boolean };
  content_scripts: Array<{ js: string[] }>;
  icons?: Record<string, string>;
  permissions?: string[];
}

const PNG_SIGNATURE = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);

function manifestReferencedFiles(manifest: ExtensionManifest): string[] {
  const referenced = [
    manifest.background.service_worker,
    manifest.action.default_popup,
    manifest.options_ui.page,
    ...manifest.content_scripts.flatMap((entry) => entry.js),
    ...Object.values(manifest.icons ?? {}),
  ];
  const defaultIcon = manifest.action.default_icon;
  if (typeof defaultIcon === "string") {
    referenced.push(defaultIcon);
  } else if (defaultIcon) {
    referenced.push(...Object.values(defaultIcon));
  }
  return referenced;
}

function runExtensionBuild(): void {
  execFileSync(
    process.execPath,
    [join(repositoryRoot, "scripts", "build-extension.mjs")],
    { cwd: repositoryRoot, stdio: "pipe" },
  );
}

function listFiles(directory: string): string[] {
  const files: string[] = [];
  for (const name of readdirSync(directory)) {
    const path = join(directory, name);
    if (statSync(path).isDirectory()) {
      files.push(...listFiles(path));
    } else {
      files.push(path);
    }
  }
  return files;
}

describe("built extension smoke test", () => {
  it("builds every manifest-referenced file with the Stage 0 selectors inlined", () => {
    runExtensionBuild();

    const manifest = JSON.parse(
      readFileSync(join(outputRoot, "manifest.json"), "utf8"),
    ) as ExtensionManifest;
    expect(manifest.options_ui.open_in_tab).toBe(true);
    expect(manifest.permissions).toContain("notifications");
    const referenced = manifestReferencedFiles(manifest);
    for (const file of referenced) {
      expect(existsSync(join(outputRoot, file)), `missing ${file}`).toBe(true);
    }

    const icon = readFileSync(join(outputRoot, "icons", "icon128.png"));
    expect(icon.subarray(0, 8)).toEqual(PNG_SIGNATURE);

    for (const htmlFile of [
      manifest.action.default_popup,
      manifest.options_ui.page,
    ]) {
      const html = readFileSync(join(outputRoot, htmlFile), "utf8");
      for (const match of html.matchAll(/(?:src|href)="([^"]+)"/g)) {
        const target = match[1];
        if (/^(?:https?:)?\/\//.test(target)) continue;
        expect(
          existsSync(join(outputRoot, target)),
          `missing ${target} referenced by ${htmlFile}`,
        ).toBe(true);
      }
    }

    for (const path of listFiles(outputRoot)) {
      const contents = readFileSync(path, "utf8");
      expect(
        unresolvedPlaceholderPattern.test(contents),
        `${path} contains an unresolved selector placeholder`,
      ).toBe(false);
    }

    const selectorsFixture = JSON.parse(
      readFileSync(
        join(fixtureRoot, "lecture-page.selectors.json"),
        "utf8",
      ),
    ) as { transcriptContainerSelector: string; transcriptButtonSelector: string };
    const contentBundle = readFileSync(join(outputRoot, "content.js"), "utf8");
    expect(contentBundle).toContain(selectorsFixture.transcriptContainerSelector);
    expect(contentBundle).toContain(selectorsFixture.transcriptButtonSelector);
    expect(contentBundle).toContain("capture_job");

    const backgroundBundle = readFileSync(
      join(outputRoot, "background.js"),
      "utf8",
    );
    expect(backgroundBundle).toContain("capture_job");

    const optionsBundle = readFileSync(join(outputRoot, "options.js"), "utf8");
    expect(optionsBundle).toContain("autoCapture");
    expect(optionsBundle).toContain("notificationsEnabled");

    const popupBundle = readFileSync(join(outputRoot, "popup.js"), "utf8");
    expect(popupBundle).toContain("openOptionsPage");
  }, 120_000);
});
