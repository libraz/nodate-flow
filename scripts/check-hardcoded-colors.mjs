#!/usr/bin/env node
// check-hardcoded-colors — fail when a colour is written as a literal
// instead of flowing through a design token.
//
// A literal colour does not change with the theme. It looks correct in
// whichever theme the author had open and wrong in the other five, and
// nothing about it reads as a mistake later: `#6e5494` is a perfectly
// ordinary-looking value.
//
// Excluded by location, because these are where colour values are
// supposed to be written out:
//
//   - packages/ui/src/themes/    theme definitions
//   - packages/ui/src/tokens/    token definitions
//   - **/*.test.* / *.spec.*     tests assert on concrete values
//
// Anything else needs an `nf-color-override: <reason>` annotation. The
// rule lives in scripts/lib/token-override.mjs, shared with the dimension
// scan; the marker does not. Both checks read one marker until it became
// clear what that costs: a spacing exemption switched this check off for
// the whole file it sat in, and 146 files reached that state without one
// colour exemption ever being written. Giving each check its own marker
// is what makes an exemption a claim about a specific thing.
//
// Usage:
//   node scripts/check-hardcoded-colors.mjs

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

import { overrideRule } from './lib/token-override.mjs';

const override = overrideRule('nf-color-override');

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');

const SCAN_DIRS = ['apps/flow-web/src', 'apps/accounts-web/src', 'packages/ui/src/primitives'];
const EXCLUDE_FRAGMENTS = [
  '/node_modules/',
  '/dist/',
  '/packages/ui/src/themes/',
  '/packages/ui/src/tokens/',
  '.test.',
  '.spec.',
];

/**
 * Hex, rgb()/rgba(), hsl()/hsla(), oklch().
 *
 * Any literal counts, not just one sitting on a colour-named property.
 * The previous version matched `(color|background|fill|…)` against the
 * whole grep line, filename included, so it flagged palette modules for
 * being called `source-colors.ts` and had to carry hand-written
 * exceptions for the constants inside them — while the same constants in
 * a file named anything else went unread.
 */
const COLOR = /#[0-9a-fA-F]{3,8}\b|\b(?:rgba?|hsla?|oklch)\s*\(/;

function walk(dir, out) {
  let entries;
  try {
    entries = readdirSync(dir);
  } catch {
    return out;
  }
  for (const entry of entries) {
    const full = join(dir, entry);
    if (EXCLUDE_FRAGMENTS.some((frag) => `${full}/`.includes(frag))) continue;
    let s;
    try {
      s = statSync(full);
    } catch {
      continue;
    }
    if (s.isDirectory()) walk(full, out);
    else if (/\.(css|tsx?)$/.test(entry) && !entry.endsWith('.d.ts')) out.push(full);
  }
  return out;
}

/**
 * A status colour used to paint text.
 *
 * `--nf-color-danger` and its three siblings are fills: bright enough to
 * read as the status at a glance, which is the opposite of what text
 * needs. Measured against each theme's background they land between
 * 1.87:1 and 6.01:1 — fifteen of the sixteen theme-and-status pairs miss
 * AA, and `warning` at 1.87 misses even the 3:1 floor for large text. The
 * `-fg` counterparts exist for exactly this and sit at 7.3:1 or better
 * everywhere.
 *
 * It went unnoticed because it is not a bug you can see in one theme: the
 * author picks the colour that means "error", and it does mean error. It
 * is just faint.
 *
 * The second pattern catches the same thing one level of indirection
 * away. A component that routes its colours through local custom
 * properties — `--sc-inactive-fg: var(--nf-color-danger)`, then
 * `color: var(--sc-inactive-fg)` — puts the assignment and the use in
 * different rules, and only the assignment says which colour it is. Four
 * segmented-control tones were painting their inactive label that way.
 */
const STATUS_AS_TEXT = /(?<![-\w])color:\s*['"]?var\(--nf-color-(danger|warning|success|info)\)/;
const STATUS_AS_FG_ALIAS = /--[\w-]*-fg:\s*var\(--nf-color-(danger|warning|success|info)\)/;

const findings = [];
const textFindings = [];
for (const root of SCAN_DIRS) {
  for (const file of walk(join(repo, root), [])) {
    const text = readFileSync(file, 'utf8');
    const hasColor = COLOR.test(text);
    const hasStatusText = STATUS_AS_TEXT.test(text) || STATUS_AS_FG_ALIAS.test(text);
    if (!hasColor && !hasStatusText) continue;
    const exempt = override.state(text);
    if (exempt.wholeFile) continue;
    const lines = text.split('\n');
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i] ?? '';
      if (exempt.lines.has(i + 1)) continue;
      // Commented-out code and prose that quotes a colour.
      if (/^\s*(\/\/|\/\*|\*)/.test(line)) continue;
      const status = line.match(STATUS_AS_TEXT) ?? line.match(STATUS_AS_FG_ALIAS);
      if (status) {
        textFindings.push({
          where: `${relative(repo, file)}:${i + 1}`,
          status: status[1],
          text: line.trim(),
        });
      }
      if (!COLOR.test(line)) continue;
      // A value already routed through a token on the same declaration.
      if (/var\(--nf-/.test(line)) continue;
      findings.push({ where: `${relative(repo, file)}:${i + 1}`, text: line.trim() });
    }
  }
}

if (findings.length > 0) {
  console.error(`check-hardcoded-colors: ${findings.length} literal colour value(s):\n`);
  for (const f of findings) console.error(`  ${f.where}\n    ${f.text}`);
  console.error(
    '\nUse var(--nf-color-*) or var(--nf-cal-*). A value that genuinely cannot flow through a',
  );
  console.error('token — an external brand hex, a value persisted by the API — takes an');
  console.error(
    '`nf-color-override: <reason>` annotation on its own line above it, or the whole-file form',
  );
  console.error('`nf-color-override-file: <reason>` when every literal in the file is exempt.');
}

if (textFindings.length > 0) {
  console.error(
    `\ncheck-hardcoded-colors: ${textFindings.length} status colour(s) painting text:\n`,
  );
  for (const f of textFindings) console.error(`  ${f.where}\n    ${f.text}`);
  console.error(
    '\nThe base status colours are fills. Against the page background they run from 1.87:1',
  );
  console.error(
    '(warning) to 6.01:1, so text painted with one is between hard and impossible to read.',
  );
  console.error('Use the `-fg` counterpart: var(--nf-color-danger-fg) and its siblings.');
}

if (findings.length > 0 || textFindings.length > 0) process.exit(1);

console.info('check-hardcoded-colors: every colour outside the theme files flows through a token');
