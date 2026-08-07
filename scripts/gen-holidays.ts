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
 * Usage: bun run scripts/gen-holidays.ts
 */
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
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

const outPath = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  'packages',
  'go-shared',
  'holidays',
  'data.json',
);

const payload = {
  fromYear: FROM_YEAR,
  toYear: TO_YEAR,
  countries,
};

writeFileSync(outPath, `${JSON.stringify(payload, null, 2)}\n`, 'utf8');

const total = Object.values(countries).reduce((n, d) => n + d.length, 0);
console.log(
  `gen-holidays: wrote ${outPath} (${Object.keys(countries).length} countries, ${total} dates, ${FROM_YEAR}-${TO_YEAR})`,
);
