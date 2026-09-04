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
// The self-verification cases run on every invocation, before the real
// scan, and a failure among them stops the run. A scan that proved it read
// files has not proved it can still recognise a colour: a pattern that
// stopped matching prints the same clean line as a tree where every value
// flows through a token.
//
// Usage:
//   node scripts/check-hardcoded-colors.mjs

import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

import { overrideRule } from './lib/token-override.mjs';

const override = overrideRule('nf-color-override');

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');

const SCAN_DIRS = [
  'apps/flow-web/src',
  'apps/accounts-web/src',
  'packages/ui/src/primitives',
  'packages/ui/src/calendar',
];
/**
 * Where the fill tokens the text check names are declared. These are
 * excluded from the scan itself — they are the files colour values belong
 * in — but they are read here to confirm the tokens still exist.
 */
const TOKEN_SOURCES = ['packages/ui/src/themes', 'packages/ui/src/tokens'];
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

function walk(dir, out, exclude = EXCLUDE_FRAGMENTS) {
  let entries;
  try {
    entries = readdirSync(dir);
  } catch {
    return out;
  }
  for (const entry of entries) {
    const full = join(dir, entry);
    if (exclude.some((frag) => `${full}/`.includes(frag))) continue;
    let s;
    try {
      s = statSync(full);
    } catch {
      continue;
    }
    if (s.isDirectory()) walk(full, out, exclude);
    else if (/\.(css|tsx?)$/.test(entry) && !entry.endsWith('.d.ts')) out.push(full);
  }
  return out;
}

/**
 * Refuse to report success over a set nothing was read into.
 *
 * The walk swallows a missing directory, so a renamed scan root yields no
 * files, no findings, and a clean bill of health for a tree that was
 * never opened. Every set this check reasons over is proved non-empty
 * first.
 */
function vacuous(lines) {
  for (const line of lines) console.error(line);
  process.exit(2);
}

/**
 * A fill colour used to paint text.
 *
 * `--nf-color-danger` and the nine tokens beside it are fills: bright
 * enough to read as the thing they mean at a glance, which is the
 * opposite of what text needs. Measured against the surface each one is
 * actually paired with, the status colours land between 1.87:1 and
 * 6.01:1, the accent reaches 2.14:1 in the aurora light theme, and the
 * five calendar tones sit between 2.17:1 and 3.50:1 there. The `-fg`
 * counterparts exist for exactly this and clear 4.5:1 everywhere.
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
const FILL_TOKENS = [
  '--nf-color-danger',
  '--nf-color-warning',
  '--nf-color-success',
  '--nf-color-info',
  '--nf-color-accent',
  '--nf-cal-task-color',
  '--nf-cal-event-color',
  '--nf-cal-block-color',
  '--nf-cal-free-color',
  '--nf-cal-milestone-color',
];
const FILLS = FILL_TOKENS.map((t) => t.replace('--', '')).join('|');
const STATUS_AS_TEXT = new RegExp(String.raw`(?<![-\w])color:\s*['"]?var\(--(${FILLS})\)`);
const STATUS_AS_FG_ALIAS = new RegExp(String.raw`--[\w-]*-fg:\s*var\(--(${FILLS})\)`);

/**
 * The verdict on one line, exemption-blind: whether it writes a literal
 * colour, and which fill token it paints text with, if any.
 *
 * `status` is read before `literal` for the same reason the two are
 * separate findings: a fill used as text is already routed through a
 * token, so the literal test has nothing to say about it.
 */
function inspectLine(line) {
  // Commented-out code and prose that quotes a colour.
  if (/^\s*(\/\/|\/\*|\*)/.test(line)) return { literal: false, status: null };
  const status = line.match(STATUS_AS_TEXT) ?? line.match(STATUS_AS_FG_ALIAS);
  // A value already routed through a token on the same declaration.
  const literal = COLOR.test(line) && !/var\(--nf-/.test(line);
  return { literal, status: status?.[1] ?? null };
}

// ---------------------------------------------------------------------------
// Self-verification. Runs before the scan, every time.
// ---------------------------------------------------------------------------

function selfCheck() {
  const cases = [
    [
      'reports a literal colour value',
      () => {
        assert.equal(inspectLine('  background: #6e5494;').literal, true);
        assert.equal(inspectLine('  background: rgba(0, 0, 0, 0.4);').literal, true);
      },
    ],
    [
      'accepts the same declaration written through a token',
      () => {
        assert.deepEqual(inspectLine('  background: var(--nf-color-surface);'), {
          literal: false,
          status: null,
        });
      },
    ],
    [
      'does not read a colour out of a comment',
      () => {
        assert.equal(inspectLine('  // background: #6e5494;').literal, false);
        assert.equal(inspectLine('   * the brand hex is #6e5494').literal, false);
      },
    ],
    [
      'reports a fill token painting text, directly and through a -fg alias',
      () => {
        assert.equal(inspectLine('  color: var(--nf-color-danger);').status, 'nf-color-danger');
        assert.equal(
          inspectLine('  --sc-inactive-fg: var(--nf-cal-task-color);').status,
          'nf-cal-task-color',
        );
        assert.equal(inspectLine('  color: var(--nf-color-danger-fg);').status, null);
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
    `check-hardcoded-colors: ${selfFailures.length} self-verification case(s) failed, so the scan was not run:\n`,
  );
  for (const f of selfFailures) console.error(f);
  console.error(
    '\nThe scanner itself is wrong. Fix it before trusting anything it says about the sources.',
  );
  process.exit(1);
}

// The fill tokens are the second set with a vacuous mode. Both text
// patterns above are built by interpolating these names, so a token that
// has been renamed away leaves a pattern that cannot match anything: the
// text check keeps running and keeps finding nothing, on a tree where
// every status label may well be painted with a fill. The names are
// checked against the files that declare them rather than assumed.
{
  const declared = new Set();
  const sources = [];
  for (const root of TOKEN_SOURCES) {
    const before = sources.length;
    walk(join(repo, root), sources, ['/node_modules/', '/dist/']);
    if (sources.length === before) {
      vacuous([
        `check-hardcoded-colors: token source ${root} holds no file, so the fill tokens the text check names could not be confirmed to exist.`,
        'Point TOKEN_SOURCES at where the theme and token declarations live now.',
      ]);
    }
  }
  for (const file of sources) {
    for (const m of readFileSync(file, 'utf8').matchAll(/(--[\w-]+)\s*:/g)) {
      if (m[1] !== undefined) declared.add(m[1]);
    }
  }
  const missing = FILL_TOKENS.filter((t) => !declared.has(t));
  if (missing.length > 0) {
    vacuous([
      `check-hardcoded-colors: ${missing.length} of ${FILL_TOKENS.length} fill token(s) named by the fill-as-text patterns are not declared anywhere in ${TOKEN_SOURCES.join(' or ')}:`,
      ...missing.map((t) => `  ${t}`),
      '',
      'A pattern built from a token nothing declares matches nothing, so that colour is no longer',
      'checked for being used as text. Update FILL_TOKENS to the names these tokens now carry.',
    ]);
  }
}

const findings = [];
const textFindings = [];
/** Root -> files scanned, so an empty root can be named rather than summed away. */
const filesByRoot = new Map();
for (const root of SCAN_DIRS) {
  const rootFiles = walk(join(repo, root), []);
  filesByRoot.set(root, rootFiles.length);
  for (const file of rootFiles) {
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
      const { literal, status } = inspectLine(line);
      if (status !== null) {
        textFindings.push({
          where: `${relative(repo, file)}:${i + 1}`,
          status,
          text: line.trim(),
        });
      }
      if (!literal) continue;
      findings.push({ where: `${relative(repo, file)}:${i + 1}`, text: line.trim() });
    }
  }
}

// Proved per root, not in total: a total stays satisfied by the roots
// that still exist while a renamed one quietly stops being scanned.
const emptyRoots = [...filesByRoot].filter(([, n]) => n === 0).map(([root]) => root);
if (emptyRoots.length > 0) {
  vacuous([
    `check-hardcoded-colors: ${emptyRoots.length} of ${SCAN_DIRS.length} scan root(s) hold no file, so nothing under them was checked:`,
    ...emptyRoots.map((root) => `  ${root}`),
    '',
    'Either the sources moved, or EXCLUDE_FRAGMENTS now excludes the whole root.',
    'Point SCAN_DIRS at where they live now.',
  ]);
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
  console.error(`\ncheck-hardcoded-colors: ${textFindings.length} fill colour(s) painting text:\n`);
  for (const f of textFindings) console.error(`  ${f.where}\n    ${f.text}`);
  console.error(
    '\nThese tokens are fills. Against the surface each is paired with they run from 1.87:1',
  );
  console.error('to 6.01:1, so text painted with one is between hard and impossible to read.');
  console.error(
    'Use the `-fg` counterpart: --nf-color-danger-fg, --nf-color-accent-fg, --nf-cal-task-fg, and so on.',
  );
}

if (findings.length > 0 || textFindings.length > 0) process.exit(1);

console.info('check-hardcoded-colors: every colour outside the theme files flows through a token');
