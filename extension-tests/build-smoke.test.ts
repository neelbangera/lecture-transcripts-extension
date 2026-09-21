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
  action: { default_popup: string };
  content_scripts: Array<{ js: string[] }>;
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
    const referenced = [
      manifest.background.service_worker,
      manifest.action.default_popup,
      ...manifest.content_scripts.flatMap((entry) => entry.js),
    ];
    for (const file of referenced) {
      expect(existsSync(join(outputRoot, file)), `missing ${file}`).toBe(true);
    }

    const popupHtml = readFileSync(join(outputRoot, "popup.html"), "utf8");
    for (const match of popupHtml.matchAll(/(?:src|href)="([^"]+)"/g)) {
      const target = match[1];
      if (/^(?:https?:)?\/\//.test(target)) continue;
      expect(existsSync(join(outputRoot, target)), `missing ${target}`).toBe(
        true,
      );
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
  }, 120_000);
});
