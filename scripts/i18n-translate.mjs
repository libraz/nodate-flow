#!/usr/bin/env node
// i18n translator bot — deterministic scaffold.
//
// Walks every `apps/*/locales/en/*.json`, finds keys that are missing in
// each sibling language, and either reports them (--check, CI gate) or
// fills them in with a `[TODO:<lang>] <english>` placeholder (--write,
// used by the GitHub Actions translator job). A future LLM path can
// replace the placeholder generator without changing the CLI contract.
//
// English is the reference in every locale root. Languages are whatever
// directories sit next to `en`, so adding a language or an app is enough
// to put it under the gate — nothing here names a language. That
// generality is the point: the check previously compared en against ja
// alone, in flow-web alone, which is why nine `generate.*` keys could go
// missing from zh/pages.json and stay missing through a full CI run.
//
// The --check mode also scans every apps/*/locales/*/*.json (and
// apps/*/src/locales/*/*.json for apps that co-locate locales with the
// source tree) for string values that are empty or whitespace-only, so a
// regressed codegen pipeline — like the one that previously shipped
// apps/flow-web/locales/ja/errors.json as 253 empty strings — fails CI
// instead of silently surfacing English copy inside the JA UI.
//
// Usage:
//   node scripts/i18n-translate.mjs --check   # exit 1 on key drift or empty values
//   node scripts/i18n-translate.mjs --write   # patch non-English files in place

import { readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');

/** The language every other one is compared against. */
const REFERENCE_LANG = 'en';

const mode = process.argv.includes('--write') ? 'write' : 'check';

function walk(obj, prefix, out) {
  for (const [k, v] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object' && !Array.isArray(v)) walk(v, path, out);
    else out.set(path, v);
  }
}

function setPath(obj, path, value) {
  const parts = path.split('.');
  let cur = obj;
  for (let i = 0; i < parts.length - 1; i++) {
    const p = parts[i];
    if (cur[p] == null || typeof cur[p] !== 'object') cur[p] = {};
    cur = cur[p];
  }
  cur[parts.at(-1)] = value;
}

function isDirectory(path) {
  try {
    return statSync(path).isDirectory();
  } catch {
    return false;
  }
}

/**
 * Every `apps/<app>/locales` directory that carries a reference language,
 * paired with the sibling languages to hold against it. Discovered from
 * the filesystem rather than listed, so a new app or a new language is
 * covered the moment its directory exists.
 */
function collectLocaleRoots() {
  const roots = [];
  const appsDir = join(repo, 'apps');
  let apps = [];
  try {
    apps = readdirSync(appsDir);
  } catch {
    return roots;
  }
  for (const app of apps.sort()) {
    for (const rel of ['locales', join('src', 'locales')]) {
      const root = join(appsDir, app, rel);
      if (!isDirectory(join(root, REFERENCE_LANG))) continue;
      const langs = readdirSync(root)
        .filter((lang) => lang !== REFERENCE_LANG && isDirectory(join(root, lang)))
        .sort();
      roots.push({ app, root, langs });
    }
  }
  return roots;
}

/**
 * Every locale JSON file under `apps/<app>/locales/<lang>/` and
 * `apps/<app>/src/locales/<lang>/`, including the reference language.
 * The value lints below hold for every language — English copy can be
 * blank or malformed too — so nothing here filters by language.
 */
function collectLocaleJsonFiles() {
  const files = [];
  const appsDir = join(repo, 'apps');
  let apps = [];
  try {
    apps = readdirSync(appsDir);
  } catch {
    return files;
  }
  for (const app of apps.sort()) {
    for (const rel of ['locales', join('src', 'locales')]) {
      const root = join(appsDir, app, rel);
      let langs = [];
      try {
        langs = readdirSync(root).sort();
      } catch {
        continue;
      }
      for (const lang of langs) {
        const leaf = join(root, lang);
        if (!isDirectory(leaf)) continue;
        for (const entry of readdirSync(leaf).sort()) {
          if (entry.endsWith('.json')) files.push(join(leaf, entry));
        }
      }
    }
  }
  return files;
}

/** Repo-relative path, for output someone can paste into an editor. */
function relative(path) {
  return path.startsWith(`${repo}/`) ? path.slice(repo.length + 1) : path;
}

function readJson(path) {
  try {
    return JSON.parse(readFileSync(path, 'utf8'));
  } catch {
    // Absent or unreadable: treat as an empty catalog so every reference
    // key is reported missing, which is what a dropped file should look
    // like. Malformed JSON is separately surfaced by the scans below.
    return {};
  }
}

let missingTotal = 0;
let staleTotal = 0;
const report = [];

for (const { root, langs } of collectLocaleRoots()) {
  const refDir = join(root, REFERENCE_LANG);
  const files = readdirSync(refDir).filter((f) => f.endsWith('.json'));

  for (const lang of langs) {
    for (const file of files) {
      const reference = readJson(join(refDir, file));
      const targetPath = join(root, lang, file);
      const target = readJson(targetPath);

      const refFlat = new Map();
      const targetFlat = new Map();
      walk(reference, '', refFlat);
      walk(target, '', targetFlat);

      const missing = [];
      for (const [path, value] of refFlat) {
        if (!targetFlat.has(path)) missing.push({ path, value });
      }
      // A key the reference no longer has is dead weight that reads as a
      // real translation to anyone editing the file.
      const stale = [];
      for (const path of targetFlat.keys()) {
        if (!refFlat.has(path)) stale.push(path);
      }

      if (missing.length === 0 && stale.length === 0) continue;
      missingTotal += missing.length;
      staleTotal += stale.length;
      report.push({ rel: relative(targetPath), missing, stale });

      if (mode === 'write' && missing.length > 0) {
        for (const { path, value } of missing) {
          setPath(target, path, typeof value === 'string' ? `[TODO:${lang}] ${value}` : value);
        }
        writeFileSync(targetPath, `${JSON.stringify(target, null, 2)}\n`);
      }
    }
  }
}

if (report.length > 0) {
  for (const { rel, missing, stale } of report) {
    if (missing.length > 0) {
      console.info(`${rel}: ${missing.length} missing key(s)`);
      for (const { path } of missing) console.info(`  - ${path}`);
    }
    if (stale.length > 0) {
      console.info(`${rel}: ${stale.length} key(s) not present in ${REFERENCE_LANG}`);
      for (const path of stale) console.info(`  + ${path}`);
    }
  }
}

// Both value lints below hold every locale file to the same rules, so
// they walk one shared listing.
const localeJsonFiles = collectLocaleJsonFiles();

// --- Empty-value lint ---------------------------------------------------
//
// Flag any string leaf that is empty or whitespace-only, in every locale
// file and every language. A blank value is invisible to the key-parity
// check above — the key is present in both locales, so the catalogs agree
// — but i18next returns "" at runtime, and the UI renders nothing: a
// blank accessible name trips an accessibility rule, and a blank error
// message shows the user an empty alert.
//
// Whitespace-only is reported separately from "" because it is invisible
// in a diff and reads as a filled-in value to anyone editing the file.

const emptyFindings = [];

function scanForEmpty(full) {
  let parsed;
  try {
    parsed = JSON.parse(readFileSync(full, 'utf8'));
  } catch (err) {
    emptyFindings.push({ file: full, path: '<root>', reason: `invalid json: ${err.message}` });
    return;
  }
  const flat = new Map();
  walk(parsed, '', flat);
  for (const [path, value] of flat) {
    if (typeof value !== 'string' || value.trim().length > 0) continue;
    emptyFindings.push({
      file: full,
      path,
      reason: value.length === 0 ? 'empty string' : 'whitespace only',
    });
  }
}

for (const file of localeJsonFiles) scanForEmpty(file);

if (emptyFindings.length > 0) {
  console.error(`\n${emptyFindings.length} empty locale value(s) found:`);
  for (const { file, path, reason } of emptyFindings) {
    console.error(`  ${relative(file)} :: ${path} (${reason})`);
  }
}

// --- Double-brace placeholder lint --------------------------------------
//
// Both web apps register `i18next-icu` as the MessageFormat backend
// (see apps/flow-web/src/i18n/index.ts and apps/accounts-web/src/i18n.ts).
// Under ICU, placeholders use a single brace — `{name}` — while
// i18next's native interpolator uses a double
// brace — `{{name}}`. If a locale value mixes the two, ICU treats `{{name}}`
// as a literal and ships the raw template to the UI (aria-labels,
// confirmation dialogs, etc.). That's the 2026-04-23 regression.
//
// Fail the build if any locale JSON under apps/*/locales/** or
// apps/*/src/locales/** contains a `{{identifier}}` token. Identifiers are
// the same subset i18next accepts (letters / digits / underscore, leading
// letter or underscore) to avoid false positives from CSS-in-JSON or
// ICU escape sequences that happen to duplicate braces.

const DOUBLE_BRACE = /\{\{[A-Za-z_][A-Za-z0-9_]*\}\}/;
const doubleBraceFindings = [];

function scanForDoubleBrace(full) {
  let parsed;
  try {
    parsed = JSON.parse(readFileSync(full, 'utf8'));
  } catch {
    // Every file here was already parsed by the empty-value scan above,
    // which reports the parse failure; skip silently to avoid duplicate
    // noise.
    return;
  }
  const flat = new Map();
  walk(parsed, '', flat);
  for (const [path, value] of flat) {
    if (typeof value !== 'string') continue;
    const match = value.match(DOUBLE_BRACE);
    if (match) {
      doubleBraceFindings.push({ file: full, path, snippet: match[0] });
    }
  }
}

for (const file of localeJsonFiles) scanForDoubleBrace(file);

if (doubleBraceFindings.length > 0) {
  console.error(
    `\n${doubleBraceFindings.length} locale value(s) use i18next-native '{{var}}' under an ICU backend:`,
  );
  for (const { file, path, snippet } of doubleBraceFindings) {
    console.error(
      `  ${relative(file)} :: ${path} (found '${snippet}', expected '${snippet.slice(1, -1)}')`,
    );
  }
}

if (
  mode === 'check' &&
  (missingTotal > 0 || staleTotal > 0 || emptyFindings.length > 0 || doubleBraceFindings.length > 0)
) {
  if (missingTotal > 0) {
    console.error(
      `\n${missingTotal} key(s) missing from non-${REFERENCE_LANG} locales. Run \`node scripts/i18n-translate.mjs --write\` to scaffold placeholders.`,
    );
  }
  if (staleTotal > 0) {
    console.error(
      `\n${staleTotal} key(s) present in a translation but not in ${REFERENCE_LANG}. Remove them, or add them to ${REFERENCE_LANG} if they are real.`,
    );
  }
  if (emptyFindings.length > 0) {
    console.error(
      `\n${emptyFindings.length} blank value(s) in locale files. A blank value passes the key-parity check above but renders as nothing, so every leaf must carry a translation (or a "[TODO:<lang>] ..." placeholder).`,
    );
  }
  if (doubleBraceFindings.length > 0) {
    console.error(
      `\n${doubleBraceFindings.length} '{{var}}' placeholder(s) in locale files. The web apps use i18next-icu, so placeholders must be single-brace ICU form ('{var}').`,
    );
  }
  process.exit(1);
}
console.info(
  mode === 'write' ? `wrote placeholders for ${missingTotal} key(s)` : 'locales up to date',
);
