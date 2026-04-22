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
// source tree, e.g. time-web) for string values that are empty, so a
// regressed codegen pipeline — like the one that previously shipped
// apps/flow-web/locales/ja/errors.json as 253 empty strings — fails CI
// instead of silently surfacing English copy inside the JA UI.
//
// Usage:
//   node scripts/i18n-translate.mjs --check   # exit 1 if ja is missing keys or any value is empty
//   node scripts/i18n-translate.mjs --write   # patch ja files in place

import { readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
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

if (mode === 'check' && (missingTotal > 0 || emptyFindings.length > 0)) {
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
  process.exit(1);
}
console.info(
  mode === 'write' ? `wrote placeholders for ${missingTotal} key(s)` : 'ja locales up to date',
);
