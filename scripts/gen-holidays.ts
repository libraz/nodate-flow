/**
 * gen-holidays — emit the public-holiday dates the Go side needs.
 *
 * The browser reads holidays straight from `@nodate-flow/holidays`, but the
 * api has to make the same judgement when it snaps a task's due date off a
 * non-working day, and it cannot call a TypeScript provider. Rather than
 * introduce a second, independently-drifting holiday source in Go, this
 * script projects the existing provider into a small embedded dataset:
 * dates only, no names or locales, because the server never renders a
 * holiday — it only asks whether a day is one.
 *
 * Regenerate with `make gen-holidays` whenever the provider's data changes
 * or the covered range needs to move. `packages/go-shared/holidays` has a
 * test that fails once the dataset stops covering the current year.
 *
 * Usage:
 *   bun run scripts/gen-holidays.ts            # regenerate the dataset
 *   bun run scripts/gen-holidays.ts --check    # compare, write nothing
 *
 * --check answers "is what is committed what this generator would produce
 * today?" by rebuilding the dataset in memory and comparing it against the
 * committed file. It is the same contract the Go generators use
 * (scripts/gen-errors.go, scripts/gen-signal-kinds.go): content comparison
 * rather than a hash of the inputs, because a bumped provider version, an
 * edited country list and a hand-edit of the JSON all leave the inputs of a
 * hash untouched. Exit status 1 on any disagreement.
 */
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { getOrCreateProvider } from '@nodate-flow/holidays';

/**
 * Inclusive year range baked into the dataset. Fixed absolute years rather
 * than an offset from "today" so that regenerating produces a byte-identical
 * file and the codegen drift check stays meaningful.
 */
const FROM_YEAR = 2020;
const TO_YEAR = 2035;

/**
 * The country list is read out of `region.SupportedCountries()` rather than
 * repeated here. A code the product offers but ships no holiday data for
 * would accept the user's setting and then ignore holidays forever, so the
 * two lists are tied together mechanically instead of by convention.
 */
function supportedCountries(): string[] {
  const src = readFileSync(
    join(
      dirname(fileURLToPath(import.meta.url)),
      '..',
      'packages',
      'go-shared',
      'region',
      'region.go',
    ),
    'utf8',
  );
  const marker = 'var supportedCountries = map[string]string{';
  const start = src.indexOf(marker);
  if (start < 0) {
    throw new Error('gen-holidays: supportedCountries map not found in region.go');
  }
  const body = src.slice(start + marker.length, src.indexOf('\n}', start));
  const codes: string[] = [];
  for (const line of body.split('\n')) {
    const quoted = line.trim();
    if (!quoted.startsWith('"')) continue;
    const code = quoted.slice(1, quoted.indexOf('"', 1));
    if (code.length === 2) codes.push(code);
  }
  if (codes.length === 0) {
    throw new Error('gen-holidays: supportedCountries map is empty');
  }
  return codes;
}

const COUNTRIES = supportedCountries();

/**
 * Holiday classifications that make a day non-working. `observance` and
 * `optional` days (Valentine's Day, Christmas Eve half-days) are ordinary
 * working days and must not move anybody's deadline.
 */
const NON_WORKING_TYPES = new Set(['public', 'bank']);

function datesFor(country: string): string[] {
  const provider = getOrCreateProvider(country);
  const dates = new Set<string>();
  for (let year = FROM_YEAR; year <= TO_YEAR; year++) {
    for (const entry of provider.holidays(year, 'en')) {
      if (NON_WORKING_TYPES.has(entry.type)) {
        dates.add(entry.date);
      }
    }
  }
  return [...dates].sort();
}

const countries: Record<string, string[]> = {};
for (const code of [...COUNTRIES].sort()) {
  const dates = datesFor(code);
  if (dates.length === 0) {
    throw new Error(`gen-holidays: no holidays produced for ${code}`);
  }
  countries[code] = dates;
}

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
const outPath = join(repoRoot, 'packages', 'go-shared', 'holidays', 'data.json');
/** Repository-relative path, so the report reads the same anywhere. */
const outLabel = relative(repoRoot, outPath);

const payload = {
  fromYear: FROM_YEAR,
  toYear: TO_YEAR,
  countries,
};

const generated = `${JSON.stringify(payload, null, 2)}\n`;

/** Shorten a line so one long entry cannot bury the rest of the report. */
function elide(line: string): string {
  const max = 100;
  const runes = [...line];
  return runes.length <= max ? line : `${runes.slice(0, max).join('')}…`;
}

/**
 * Name the line where the committed file and the freshly generated one
 * diverge, so a failure points at a place rather than only at a file.
 */
function firstDifference(committed: string, fresh: string): string {
  const oldLines = committed.split('\n');
  const newLines = fresh.split('\n');
  const n = Math.max(oldLines.length, newLines.length);
  for (let i = 0; i < n; i++) {
    const oldLine = oldLines[i] ?? '';
    const newLine = newLines[i] ?? '';
    if (oldLine === newLine) continue;
    return [
      `    first difference at line ${i + 1}`,
      `      committed:   ${elide(oldLine)}`,
      `      regenerated: ${elide(newLine)}`,
    ].join('\n');
  }
  return '    content differs';
}

const total = Object.values(countries).reduce((n, d) => n + d.length, 0);

if (process.argv.slice(2).includes('--check')) {
  let committed: string | null = null;
  try {
    committed = readFileSync(outPath, 'utf8');
  } catch {
    committed = null;
  }
  if (committed === null) {
    console.error(
      `gen-holidays: the generator produces ${outLabel}, but it is not committed\n\n` +
        "Run 'make gen-holidays' and commit the result.",
    );
    process.exit(1);
  }
  if (committed !== generated) {
    console.error(
      `gen-holidays: this file is not what the holiday provider generates:\n\n  ${outLabel}\n` +
        `${firstDifference(committed, generated)}\n\n` +
        "Run 'make gen-holidays' and commit the result. Editing the dataset by hand does not " +
        'survive the next run — the holiday provider and the supported-country list are the source.',
    );
    process.exit(1);
  }
  console.log(
    `gen-holidays: ${outLabel} matches the holiday provider (${Object.keys(countries).length} countries, ${total} dates, ${FROM_YEAR}-${TO_YEAR})`,
  );
} else {
  writeFileSync(outPath, generated, 'utf8');
  console.log(
    `gen-holidays: wrote ${outLabel} (${Object.keys(countries).length} countries, ${total} dates, ${FROM_YEAR}-${TO_YEAR})`,
  );
}
