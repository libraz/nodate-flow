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
// The self-verification cases run on every invocation, before either file
// is read, and a failure among them stops the run. The entry-count floor
// below proves both literals were found; it says nothing about whether the
// comparison can still tell two lists apart, and a comparison that cannot
// prints the same closing line as two lists that genuinely agree.
//
// Usage:
//   node scripts/check-region-parity.mjs

import assert from 'node:assert/strict';
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

/** `"JP": "Japan",` — the Go map's entry shape. */
const GO_ENTRY = /^"([^"]+)"\s*:\s*"([^"]*)"\s*,?$/;
/** `['JP', 'Japan'],` — the TS array's entry shape. */
const TS_ENTRY = /^\[\s*'([^']+)'\s*,\s*'([^']*)'\s*\]\s*,?$/;

/**
 * One entry per line, into a code -> name map.
 *
 * A line that does not parse is collected rather than skipped: a parser
 * that silently dropped what it did not recognise would shrink both lists
 * towards the empty set, which every comparison below is vacuously true
 * over.
 */
function parseEntries(body, entryShape) {
  const entries = new Map();
  const unparsable = [];
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (trimmed === '' || trimmed.startsWith('//')) continue;
    const found = trimmed.match(entryShape);
    if (found === null) {
      unparsable.push(line);
      continue;
    }
    entries.set(found[1], found[2]);
  }
  return { entries, unparsable };
}

/** Parse one side, or report every line that did not parse and stop. */
function parseSide(path, marker, terminator, entryShape) {
  const body = literalBody(readFileSync(path, 'utf-8'), path, marker, terminator);
  const { entries, unparsable } = parseEntries(body, entryShape);
  if (unparsable.length > 0) {
    for (const line of unparsable) {
      console.error(`check-region-parity: ${path}: unparsable entry ${JSON.stringify(line)}`);
    }
    process.exit(1);
  }
  return entries;
}

/** Every way the two lists can disagree. */
function compare(go, ts) {
  return {
    onlyGo: [...go.keys()].filter((code) => !ts.has(code)).sort(),
    onlyTs: [...ts.keys()].filter((code) => !go.has(code)).sort(),
    mismatched: [...go.entries()]
      .filter(([code, name]) => ts.has(code) && ts.get(code) !== name)
      .map(
        ([code, name]) => `${code}: go=${JSON.stringify(name)} ts=${JSON.stringify(ts.get(code))}`,
      )
      .sort(),
    malformed: [...go.keys(), ...ts.keys()].filter((code) => !/^[A-Z]{2}$/.test(code)).sort(),
  };
}

// ---------------------------------------------------------------------------
// Self-verification. Runs before either source is read, every time.
// ---------------------------------------------------------------------------

function selfCheck() {
  const cases = [
    [
      'reads an entry written in each language own shape',
      () => {
        assert.deepEqual(
          [...parseEntries('\t"JP": "Japan",\n\t"FR": "France",\n', GO_ENTRY).entries],
          [
            ['JP', 'Japan'],
            ['FR', 'France'],
          ],
        );
        assert.deepEqual(
          [...parseEntries("  ['JP', 'Japan'],\n  ['FR', 'France'],\n", TS_ENTRY).entries],
          [
            ['JP', 'Japan'],
            ['FR', 'France'],
          ],
        );
      },
    ],
    [
      'collects a line it cannot read instead of dropping it',
      () => {
        const parsed = parseEntries(
          '\t"JP": "Japan",\n\t"XX" = "Nowhere",\n\n\t// a note\n',
          GO_ENTRY,
        );
        assert.deepEqual(parsed.unparsable, ['\t"XX" = "Nowhere",']);
        assert.equal(parsed.entries.size, 1);
      },
    ],
    [
      'reports a code that only one side carries, in both directions',
      () => {
        const seen = compare(
          new Map([
            ['JP', 'Japan'],
            ['FR', 'France'],
          ]),
          new Map([
            ['JP', 'Japan'],
            ['DE', 'Germany'],
          ]),
        );
        assert.deepEqual(seen.onlyGo, ['FR']);
        assert.deepEqual(seen.onlyTs, ['DE']);
        assert.deepEqual(seen.mismatched, []);
      },
    ],
    [
      'reports a name that differs and a code that is not alpha-2, and passes two identical lists',
      () => {
        const named = compare(new Map([['JP', 'Japan']]), new Map([['JP', 'Nippon']]));
        assert.deepEqual(named.mismatched, ['JP: go="Japan" ts="Nippon"']);
        const cased = compare(new Map([['jp', 'Japan']]), new Map([['jp', 'Japan']]));
        assert.deepEqual(cased.malformed, ['jp', 'jp']);
        const agreed = compare(new Map([['JP', 'Japan']]), new Map([['JP', 'Japan']]));
        assert.deepEqual(agreed, { onlyGo: [], onlyTs: [], mismatched: [], malformed: [] });
      },
    ],
  ];

  const failures = [];
  for (const [name, run] of cases) {
    try {
      run();
    } catch (err) {
      failures.push(`  ${name}\n    ${err instanceof Error ? err.message : String(err)}`);
    }
  }
  return failures;
}

const selfFailures = selfCheck();
if (selfFailures.length > 0) {
  console.error(
    `check-region-parity: ${selfFailures.length} self-verification case(s) failed, so neither list was read:\n`,
  );
  for (const f of selfFailures) console.error(f);
  console.error(
    '\nThe comparison itself is wrong. Fix it before trusting anything it says about the two lists.',
  );
  process.exit(1);
}

const go = parseSide(GO_SOURCE, GO_MARKER, '\n}', GO_ENTRY);
const ts = parseSide(TS_SOURCE, TS_MARKER, '\n];', TS_ENTRY);

// Guards the guard: a parser that quietly matched nothing would make every
// comparison below vacuously true.
if (go.size < 20 || ts.size < 20) {
  console.error(
    `check-region-parity: parsed ${go.size} Go and ${ts.size} TS entries — the literals moved, and this check is no longer reading them.`,
  );
  process.exit(1);
}

const { onlyGo, onlyTs, mismatched, malformed } = compare(go, ts);

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
