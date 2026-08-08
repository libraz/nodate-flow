#!/usr/bin/env node
// check-region-parity — fail when the frontend's country allowlist and the
// backend's disagree.
//
// The same list exists twice: `supportedCountries` in
// `packages/go-shared/region/region.go`, which is what the API validates a
// profile against, and `SUPPORTED_COUNTRIES` in `packages/sdk/src/region.ts`,
// which is what the settings form offers. Nothing generates one from the
// other.
//
// Neither direction of drift produces an error anyone sees. A code only the
// frontend knows renders a selectable option that fails on save with a
// validation error naming a field the reader did not touch. A code only the
// backend knows is simply unreachable — the country is supported, and no
// screen will ever let anyone pick it. A display name that differs is worse
// still, because both sides accept the value and only the words disagree.
//
// This lives in scripts/ rather than in the SDK's vitest suite on purpose:
// the check has to read a Go file outside `packages/sdk`, and the SDK ships
// to browser bundles with `"types": []` so that shipped code cannot reach
// for Node globals. Pulling `node:fs` into that suite would hand the whole
// package Node types back. See the note in
// `packages/sdk/src/__tests__/public-paths.test.ts`.
//
// Usage:
//   node scripts/check-region-parity.mjs

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');

const GO_SOURCE = join(repo, 'packages/go-shared/region/region.go');
const TS_SOURCE = join(repo, 'packages/sdk/src/region.ts');

const GO_MARKER = 'var supportedCountries = map[string]string{';
const TS_MARKER = 'const COUNTRY_ENTRIES: ReadonlyArray<readonly [string, string]> = [';

/** Body of a delimited literal that starts at `marker` and ends at `terminator`. */
function literalBody(source, path, marker, terminator) {
  const start = source.indexOf(marker);
  if (start === -1) {
    console.error(`check-region-parity: ${path}: cannot find ${JSON.stringify(marker)}`);
    process.exit(1);
  }
  const from = start + marker.length;
  const end = source.indexOf(terminator, from);
  if (end === -1) {
    console.error(`check-region-parity: ${path}: unterminated literal after ${marker}`);
    process.exit(1);
  }
  return source.slice(from, end);
}

/** `"JP": "Japan",` -> map entry `JP -> Japan`. */
function parseGo() {
  const body = literalBody(readFileSync(GO_SOURCE, 'utf-8'), GO_SOURCE, GO_MARKER, '\n}');
  const out = new Map();
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (trimmed === '' || trimmed.startsWith('//')) continue;
    const match = /^"([^"]+)"\s*:\s*"([^"]*)"\s*,?$/.exec(trimmed);
    if (!match) {
      console.error(`check-region-parity: ${GO_SOURCE}: unparsable entry ${JSON.stringify(line)}`);
      process.exit(1);
    }
    out.set(match[1], match[2]);
  }
  return out;
}

/** `['JP', 'Japan'],` -> map entry `JP -> Japan`. */
function parseTs() {
  const body = literalBody(readFileSync(TS_SOURCE, 'utf-8'), TS_SOURCE, TS_MARKER, '\n];');
  const out = new Map();
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (trimmed === '' || trimmed.startsWith('//')) continue;
    const match = /^\[\s*'([^']+)'\s*,\s*'([^']*)'\s*\]\s*,?$/.exec(trimmed);
    if (!match) {
      console.error(`check-region-parity: ${TS_SOURCE}: unparsable entry ${JSON.stringify(line)}`);
      process.exit(1);
    }
    out.set(match[1], match[2]);
  }
  return out;
}

const go = parseGo();
const ts = parseTs();

// Guards the guard: a parser that quietly matched nothing would make every
// comparison below vacuously true.
if (go.size < 20 || ts.size < 20) {
  console.error(
    `check-region-parity: parsed ${go.size} Go and ${ts.size} TS entries — the literals moved, and this check is no longer reading them.`,
  );
  process.exit(1);
}

const onlyGo = [...go.keys()].filter((code) => !ts.has(code)).sort();
const onlyTs = [...ts.keys()].filter((code) => !go.has(code)).sort();
const mismatched = [...go.entries()]
  .filter(([code, name]) => ts.has(code) && ts.get(code) !== name)
  .map(([code, name]) => `${code}: go=${JSON.stringify(name)} ts=${JSON.stringify(ts.get(code))}`)
  .sort();
const malformed = [...go.keys(), ...ts.keys()].filter((code) => !/^[A-Z]{2}$/.test(code)).sort();

if (onlyGo.length > 0) {
  console.error(
    `\ncheck-region-parity: ${onlyGo.length} country/countries the API accepts that no screen offers:`,
  );
  for (const code of onlyGo) console.error(`  ${code} (${go.get(code)})`);
}
if (onlyTs.length > 0) {
  console.error(
    `\ncheck-region-parity: ${onlyTs.length} country/countries the settings form offers that the API rejects on save:`,
  );
  for (const code of onlyTs) console.error(`  ${code} (${ts.get(code)})`);
}
if (mismatched.length > 0) {
  console.error(`\ncheck-region-parity: ${mismatched.length} display name(s) disagree:`);
  for (const line of mismatched) console.error(`  ${line}`);
}
if (malformed.length > 0) {
  console.error(
    `\ncheck-region-parity: ${malformed.length} code(s) are not ISO 3166-1 alpha-2: ${malformed.join(', ')}`,
  );
}

if (onlyGo.length > 0 || onlyTs.length > 0 || mismatched.length > 0 || malformed.length > 0) {
  console.error(
    '\nEdit both packages/go-shared/region/region.go and packages/sdk/src/region.ts; the holidays package has to ship data for anything added.',
  );
  process.exit(1);
}

console.info(`check-region-parity: ${go.size} countries, Go and TS agree on every code and name`);
