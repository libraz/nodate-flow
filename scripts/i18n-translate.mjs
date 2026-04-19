#!/usr/bin/env node
// 2.AI-10 i18n translator bot — deterministic scaffold.
//
// Walks apps/flow-web/locales/en/*.json, finds keys that are missing in the
// matching ja file, and either reports them (--check, CI gate) or fills
// them in with a `[TODO:ja] <english>` placeholder (--write, used by the
// GitHub Actions translator job). A future LLM path can replace the
// placeholder generator without changing the CLI contract.
//
// Usage:
//   node scripts/i18n-translate.mjs --check   # exit 1 if ja is missing keys
//   node scripts/i18n-translate.mjs --write   # patch ja files in place

import { readFileSync, readdirSync, writeFileSync } from 'node:fs';
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

if (mode === 'check' && missingTotal > 0) {
  console.error(
    `\n${missingTotal} key(s) missing in ja locales. Run \`node scripts/i18n-translate.mjs --write\` to scaffold placeholders.`,
  );
  process.exit(1);
}
console.info(
  mode === 'write' ? `wrote placeholders for ${missingTotal} key(s)` : 'ja locales up to date',
);
