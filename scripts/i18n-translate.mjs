#!/usr/bin/env node
// 2.AI-10 i18n translator bot — deterministic scaffold.
//
// Walks apps/flow-web/locales/en/*.json, finds keys that are missing in the
// matching ja file, and either reports them (--check, CI gate) or fills
// them in with a `[TODO:ja] <english>` placeholder (--write, used by the
// GitHub Actions translator job). A future LLM path can replace the
// placeholder generator without changing the CLI contract.
//
// The --check mode also scans every apps/*/locales/*/*.json (and
// apps/*/src/locales/*/*.json for apps that co-locate locales with the
// source tree) for string values that are empty, so a regressed codegen
// pipeline — like the one that previously shipped
// apps/flow-web/locales/ja/errors.json as 253 empty strings — fails CI
// instead of silently surfacing English copy inside the JA UI.
//
// Usage:
//   node scripts/i18n-translate.mjs --check   # exit 1 if ja is missing keys or any value is empty
//   node scripts/i18n-translate.mjs --write   # patch ja files in place

import { readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');
const enDir = join(repo, 'apps/flow-web/locales/en');
const jaDir = join(repo, 'apps/flow-web/locales/ja');

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

const files = readdirSync(enDir).filter((f) => f.endsWith('.json'));
let missingTotal = 0;
const report = [];

for (const file of files) {
  const en = JSON.parse(readFileSync(join(enDir, file), 'utf8'));
  let ja = {};
  try {
    ja = JSON.parse(readFileSync(join(jaDir, file), 'utf8'));
  } catch {
    // ja file does not exist yet; treat as empty.
  }
  const enFlat = new Map();
  const jaFlat = new Map();
  walk(en, '', enFlat);
  walk(ja, '', jaFlat);

  const missing = [];
  for (const [path, value] of enFlat) {
    if (!jaFlat.has(path)) missing.push({ path, value });
  }
  if (missing.length === 0) continue;
  missingTotal += missing.length;
  report.push({ file, missing });

  if (mode === 'write') {
    for (const { path, value } of missing) {
      setPath(ja, path, typeof value === 'string' ? `[TODO:ja] ${value}` : value);
    }
    writeFileSync(join(jaDir, file), `${JSON.stringify(ja, null, 2)}\n`);
  }
}

if (report.length > 0) {
  for (const { file, missing } of report) {
    console.info(`${file}: ${missing.length} missing key(s)`);
    for (const { path } of missing) console.info(`  - ${path}`);
  }
}

// --- Empty-value lint ---------------------------------------------------
//
// Scan every apps/*/locales/<lang>/errors.json and flag any string leaf
// whose value is "". Empty values are indistinguishable from a real
// translation at runtime, so i18next returns "" and the UI falls back to
// the English server message — exactly the failure mode that shipped
// ja/errors.json as 253 empty strings. Scope is intentionally limited to
// errors.json (the codegen'd catalog) so we only catch regressions in the
// file this check exists to guard; hand-authored common.json / etc. are
// caught by the existing missing-key check above.

const emptyFindings = [];

function collectErrorsJsonFiles() {
  const files = [];
  const appsDir = join(repo, 'apps');
  let apps = [];
  try {
    apps = readdirSync(appsDir);
  } catch {
    return files;
  }
  for (const app of apps) {
    const candidates = [join(appsDir, app, 'locales'), join(appsDir, app, 'src', 'locales')];
    for (const root of candidates) {
      let langs = [];
      try {
        langs = readdirSync(root);
      } catch {
        continue;
      }
      for (const lang of langs) {
        const leaf = join(root, lang);
        let s;
        try {
          s = statSync(leaf);
        } catch {
          continue;
        }
        if (!s.isDirectory()) continue;
        const candidate = join(leaf, 'errors.json');
        try {
          statSync(candidate);
        } catch {
          continue;
        }
        files.push(candidate);
      }
    }
  }
  return files;
}

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
    if (typeof value === 'string' && value.length === 0) {
      emptyFindings.push({ file: full, path, reason: 'empty string' });
    }
  }
}

for (const file of collectErrorsJsonFiles()) scanForEmpty(file);

if (emptyFindings.length > 0) {
  console.error(`\n${emptyFindings.length} empty locale value(s) found:`);
  for (const { file, path, reason } of emptyFindings) {
    // Trim repo prefix for readable output.
    const rel = file.startsWith(`${repo}/`) ? file.slice(repo.length + 1) : file;
    console.error(`  ${rel} :: ${path} (${reason})`);
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

function collectAllLocaleJsonFiles() {
  const files = [];
  const appsDir = join(repo, 'apps');
  let apps = [];
  try {
    apps = readdirSync(appsDir);
  } catch {
    return files;
  }
  for (const app of apps) {
    const roots = [join(appsDir, app, 'locales'), join(appsDir, app, 'src', 'locales')];
    for (const root of roots) {
      let langs = [];
      try {
        langs = readdirSync(root);
      } catch {
        continue;
      }
      for (const lang of langs) {
        const leaf = join(root, lang);
        let s;
        try {
          s = statSync(leaf);
        } catch {
          continue;
        }
        if (!s.isDirectory()) continue;
        let entries = [];
        try {
          entries = readdirSync(leaf);
        } catch {
          continue;
        }
        for (const entry of entries) {
          if (!entry.endsWith('.json')) continue;
          files.push(join(leaf, entry));
        }
      }
    }
  }
  return files;
}

function scanForDoubleBrace(full) {
  let parsed;
  try {
    parsed = JSON.parse(readFileSync(full, 'utf8'));
  } catch {
    // JSON errors are reported by the errors.json scan above or by
    // upstream tooling; skip silently here to avoid duplicate noise.
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

for (const file of collectAllLocaleJsonFiles()) scanForDoubleBrace(file);

if (doubleBraceFindings.length > 0) {
  console.error(
    `\n${doubleBraceFindings.length} locale value(s) use i18next-native '{{var}}' under an ICU backend:`,
  );
  for (const { file, path, snippet } of doubleBraceFindings) {
    const rel = file.startsWith(`${repo}/`) ? file.slice(repo.length + 1) : file;
    console.error(`  ${rel} :: ${path} (found '${snippet}', expected '${snippet.slice(1, -1)}')`);
  }
}

if (
  mode === 'check' &&
  (missingTotal > 0 || emptyFindings.length > 0 || doubleBraceFindings.length > 0)
) {
  if (missingTotal > 0) {
    console.error(
      `\n${missingTotal} key(s) missing in ja locales. Run \`node scripts/i18n-translate.mjs --write\` to scaffold placeholders.`,
    );
  }
  if (emptyFindings.length > 0) {
    console.error(
      `\n${emptyFindings.length} empty string value(s) in locale files. Every leaf must carry a translation (or a "[TODO:ja] ..." placeholder).`,
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
  mode === 'write' ? `wrote placeholders for ${missingTotal} key(s)` : 'ja locales up to date',
);
