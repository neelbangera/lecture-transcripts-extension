/**
 * Build the unpacked Chrome extension into dist/extension.
 *
 * The Stage 0 selector fixture is validated and embedded into the content
 * bundle at build time (__STAGE0_SELECTORS__); the built extension never
 * reads a fixture path at runtime.
 */

import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { build } from "esbuild";

const repositoryRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const extensionRoot = join(repositoryRoot, "extension");
const sourceRoot = join(extensionRoot, "src");
const fixtureRoot = join(repositoryRoot, "extension-tests", "fixtures");
const outputRoot = join(repositoryRoot, "dist", "extension");

function fail(message) {
  throw new Error(`build-extension: ${message}`);
}

function requireString(object, key, label) {
  const value = object[key];
  if (typeof value !== "string" || value.length === 0) {
    fail(`${label}.${key} must be a nonempty string`);
  }
  return value;
}

function requireObject(object, key, label) {
  const value = object[key];
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    fail(`${label}.${key} must be an object`);
  }
  return value;
}

async function readJson(path, label) {
  let text;
  try {
    text = await readFile(path, "utf8");
  } catch {
    fail(`missing ${label} at ${path}`);
  }
  try {
    return JSON.parse(text);
  } catch {
    fail(`${label} is not valid JSON: ${path}`);
  }
}

function validateSelectors(selectors) {
  const label = "lecture-page.selectors.json";
  requireString(selectors, "transcriptButtonSelector", label);
  requireString(selectors, "transcriptContainerSelector", label);
  requireString(selectors, "timestampFormat", label);
  for (const key of ["courseSelector", "termSelector", "lectureNumberSelector"]) {
    const capture = requireObject(selectors, key, label);
    requireString(capture, "selector", `${label}.${key}`);
    requireString(capture, "regex", `${label}.${key}`);
  }
  const dateSource = requireObject(selectors, "lectureDateSource", label);
  requireString(dateSource, "dateSelector", `${label}.lectureDateSource`);
  requireString(dateSource, "dateRegex", `${label}.lectureDateSource`);
  const completion = requireObject(selectors, "completionIndicator", label);
  requireString(completion, "selector", `${label}.completionIndicator`);
  requireString(completion, "mode", `${label}.completionIndicator`);
  if (!Array.isArray(selectors.loadingTextMarkers)) {
    fail(`${label}.loadingTextMarkers must be an array`);
  }
  if (typeof selectors.stabilityDebounceMs !== "number" || selectors.stabilityDebounceMs <= 0) {
    fail(`${label}.stabilityDebounceMs must be a positive number`);
  }
  if (typeof selectors.sanityMinChars !== "number" || selectors.sanityMinChars < 1) {
    fail(`${label}.sanityMinChars must be a positive number`);
  }
  return selectors;
}

async function buildBundle(entry, format) {
  await build({
    entryPoints: [join(sourceRoot, entry)],
    outfile: join(outputRoot, entry.replace(/\.ts$/, ".js")),
    bundle: true,
    format,
    platform: "browser",
    target: ["chrome120"],
    sourcemap: false,
    logLevel: "warning",
    define: {
      __STAGE0_SELECTORS__: JSON.stringify(selectorsFixture),
    },
  });
}

const selectorsFixture = validateSelectors(
  await readJson(join(fixtureRoot, "lecture-page.selectors.json"), "selector fixture"),
);
const courseMapping = await readJson(
  join(fixtureRoot, "course-mapping.json"),
  "course mapping fixture",
);
if (!Array.isArray(courseMapping.courseMappings) || courseMapping.courseMappings.length === 0) {
  fail("course-mapping.json must contain a nonempty courseMappings array");
}

const packageJson = await readJson(join(repositoryRoot, "package.json"), "package.json");
const manifest = await readJson(join(extensionRoot, "manifest.json"), "extension manifest");
if (typeof manifest.version !== "string" || manifest.version.length === 0) {
  fail("manifest.json.version must be a nonempty string");
}
manifest.version = packageJson.version;

await rm(outputRoot, { recursive: true, force: true });
await mkdir(outputRoot, { recursive: true });

await buildBundle("content.ts", "iife");
await buildBundle("background.ts", "esm");
await buildBundle("popup.ts", "esm");

for (const file of ["popup.html", "popup.css"]) {
  const contents = await readFile(join(extensionRoot, file), "utf8");
  await writeFile(join(outputRoot, file), contents);
}
await writeFile(join(outputRoot, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);

console.log(`build-extension: wrote ${outputRoot}`);
