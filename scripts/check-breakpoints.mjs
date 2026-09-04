#!/usr/bin/env node
// check-breakpoints — hold hand-written media queries to the declared
// breakpoint scale.
//
// The scale is declared once, in
// `packages/ui/src/tokens/breakpoints.ts`, and the runtime reads it from
// there (`(max-width: ${BP.md - 1}px)`). CSS cannot: custom properties do
// not work in at-rule conditions, so every `@media` in a stylesheet
// restates the number by hand. Nothing checked that the restatement
// matched, and it had drifted: eight rules were written at the
// breakpoint itself rather than one pixel below it, which put the CSS
// and the JS on opposite sides of the boundary pixel — at exactly 768px
// the script said "not mobile" while the stylesheet laid out mobile.
//
// Rules enforced here:
//
//   - `max-width` must be `<breakpoint> - 1`. That is the "below this
//     breakpoint" form, and it is what the JS builds.
//   - `min-width` must be exactly a breakpoint. That is the "from this
//     breakpoint up" form.
//   - Widths are written in px, matching the declaration and the runtime
//     queries. A rem query answers to the reader's font size while the
//     JS does not, so mixing them makes the two disagree for anyone who
//     changed their default font size.
//
// A threshold that several places want needs the scale extended, not an
// exception — a breakpoint nobody declared is a breakpoint nobody else
// can align to. A threshold that exactly one place wants is the opposite
// case: declaring it would dress a one-off up as an established step,
// and the next person would reach for it somewhere it does not belong.
// Those carry `nf-breakpoint-override: <reason>` instead, which records
// what the number means where it is used.
//
// The annotation covers its own line and the next, and the reason is
// required — an exemption whose justification nobody wrote down is
// indistinguishable from an oversight.
//
// Write it on its own line directly above the at-rule. Trailing it after
// the opening `{` reads well but does not survive: the formatter moves a
// comment written there onto the next line, which puts the at-rule
// outside the annotation's window and silently retires the exemption.
// (Measured against biome: the at-rule-trailing position is the only one
// it relocates. Above the at-rule, and trailing a declaration, both stay
// put.) The window is deliberately not widened backwards to absorb the
// move — an annotation written for the first declaration inside a block
// would then start exempting the block's own prelude as well — so the
// check names the cause instead, and reports any annotation that ends up
// exempting nothing.
//
// Usage:
//   node scripts/check-breakpoints.mjs

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');

const SOURCE_ROOTS = ['apps/flow-web/src', 'apps/accounts-web/src', 'packages/ui/src'];
const DECLARATION = 'packages/ui/src/tokens/breakpoints.ts';

/** Read the breakpoint scale from its single declaration. */
function readBreakpoints() {
  let src;
  try {
    src = readFileSync(join(repo, DECLARATION), 'utf8');
  } catch {
    // Reported by the caller, which can say what the empty scale costs.
    return new Map();
  }
  const body = src.slice(src.indexOf('export const BP'), src.indexOf('} as const'));
  const out = new Map();
  for (const m of body.matchAll(/(\w+)\s*:\s*(\d+)\s*,/g)) {
    out.set(m[1], Number(m[2]));
  }
  return out;
}

function walk(dir, out) {
  let entries;
  try {
    entries = readdirSync(dir);
  } catch {
    return out;
  }
  for (const entry of entries) {
    if (entry === 'node_modules' || entry === 'dist') continue;
    const full = join(dir, entry);
    let s;
    try {
      s = statSync(full);
    } catch {
      continue;
    }
    // .tsx is included: some CSS lives in template literals.
    if (s.isDirectory()) walk(full, out);
    else if (/\.(css|tsx?)$/.test(entry)) out.push(full);
  }
  return out;
}

/**
 * Refuse to report success over a set nothing was read into.
 *
 * The walk swallows a missing directory, so a renamed source root
 * contributes no file, no query, and no finding — the same output as a
 * tree that is entirely correct.
 */
function vacuous(lines) {
  for (const line of lines) console.error(line);
  process.exit(2);
}

const bp = readBreakpoints();
// The scale is the set every verdict here is measured against. It is
// recovered from the declaration by looking for two literal markers, so a
// refactor of that file can leave it empty; an empty scale cannot
// validate a single query.
if (bp.size === 0) {
  vacuous([
    `check-breakpoints: no breakpoint could be read from ${DECLARATION}, so no query had anything to be measured against.`,
    'The parse reads the entries between `export const BP` and `} as const`; that file is missing or no longer has that shape.',
  ]);
}

const byMaxWidth = new Map(); // breakpoint - 1 -> name
const byMinWidth = new Map(); // breakpoint     -> name
for (const [name, px] of bp) {
  byMaxWidth.set(px - 1, name);
  byMinWidth.set(px, name);
}

const CONDITION = /@(?:media|container)[^{;]*?\((max|min)-width:\s*([^)]+?)\s*\)/g;
/**
 * A reason is required, and it has to be words.
 *
 * Matching "any non-space after the colon" is not enough: in
 * `/* nf-breakpoint-override: *\/` the comment's own closing `*` satisfies
 * it, so a marker with the justification deleted still silences the
 * check. The reason must contain letters that are not comment syntax.
 */
const OVERRIDE = /nf-breakpoint-override:[^\S\n]*(?![*/]\s*$)[A-Za-z][^\n]*[A-Za-z]/;
const findings = [];

/** 1-based lines carrying an annotation. */
function annotationLines(lines) {
  const out = [];
  for (let i = 0; i < lines.length; i++) {
    if (OVERRIDE.test(lines[i] ?? '')) out.push(i + 1);
  }
  return out;
}

/**
 * Every query on one line that does not match the scale. Exemption-blind.
 *
 * Also tallies the queries it examined. A run that examined none reports
 * the same "every media query matches" as a run over a correct tree, so
 * the tally is what tells those apart.
 */
function violationsOn(line, where) {
  const out = [];
  for (const m of line.matchAll(CONDITION)) {
    conditionCount += 1;
    const kind = m[1];
    const raw = m[2] ?? '';
    const px = /^(\d+(?:\.\d+)?)px$/.test(raw)
      ? Number(raw.replace('px', ''))
      : /^(\d+(?:\.\d+)?)rem$/.test(raw)
        ? Number(raw.replace('rem', '')) * 16
        : null;
    if (px === null) {
      out.push({ where, raw, kind, why: 'not a plain px/rem length' });
      continue;
    }
    if (!raw.endsWith('px')) {
      out.push({
        where,
        raw,
        kind,
        why: `write it in px (${px}px); the declaration and the runtime queries are px, and a rem query moves with the reader's font size while they do not`,
      });
      continue;
    }
    const table = kind === 'max' ? byMaxWidth : byMinWidth;
    if (!table.has(px)) {
      const expected = [...table.keys()].sort((a, b) => a - b).join(' / ');
      out.push({
        where,
        raw,
        kind,
        why:
          kind === 'max'
            ? `not one below a declared breakpoint (expected ${expected})`
            : `not a declared breakpoint (expected ${expected})`,
      });
    }
  }
  return out;
}

const dangling = [];
/** Root -> files walked, so an empty root can be named rather than summed away. */
const filesByRoot = new Map();
/** Queries examined, including the ones that match the scale. */
let conditionCount = 0;

for (const root of SOURCE_ROOTS) {
  const rootFiles = walk(join(repo, root), []);
  filesByRoot.set(root, rootFiles.length);
  for (const file of rootFiles) {
    const text = readFileSync(file, 'utf8');
    if (!text.includes('-width:')) continue;
    const lines = text.split('\n');
    const annotations = annotationLines(lines);
    // An annotation exempts its own line and the one after it, so it can
    // trail a query or sit above it.
    const exempt = new Set(annotations.flatMap((n) => [n, n + 1]));
    // An annotation counts as used only when it suppressed a real
    // violation. Sitting next to a query that already matches the scale
    // does not count: that annotation is claiming something untrue.
    const used = new Set();
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i] ?? '';
      // Skip prose that quotes a query rather than writing one.
      if (/^\s*(\*|\/\/)/.test(line)) continue;
      const violations = violationsOn(line, `${relative(repo, file)}:${i + 1}`);
      if (violations.length === 0) continue;
      if (exempt.has(i + 1)) {
        if (annotations.includes(i + 1)) used.add(i + 1);
        if (annotations.includes(i)) used.add(i);
        continue;
      }
      // An offending at-rule with an annotation on the line below it is
      // almost always one the formatter relocated out of range.
      const moved = line.trimStart().startsWith('@') && OVERRIDE.test(lines[i + 1] ?? '');
      for (const v of violations) {
        findings.push(
          moved
            ? {
                ...v,
                hint: `an nf-breakpoint-override sits on line ${i + 2}, inside the block, where it no longer covers this line. The formatter moves a comment written after \`{\` onto the next line; put it on its own line directly above the at-rule instead.`,
              }
            : v,
        );
      }
    }
    for (const n of annotations) {
      if (used.has(n)) continue;
      dangling.push({
        where: `${relative(repo, file)}:${n}`,
        context: (lines[n - 1] ?? '').trim(),
      });
    }
  }
}

// Proved per root, not in total: a total stays satisfied by the roots
// that still exist while a renamed one quietly stops being walked.
const emptyRoots = [...filesByRoot].filter(([, n]) => n === 0).map(([root]) => root);
if (emptyRoots.length > 0) {
  vacuous([
    `check-breakpoints: ${emptyRoots.length} of ${SOURCE_ROOTS.length} source root(s) hold no file, so no query under them was checked:`,
    ...emptyRoots.map((root) => `  ${root}`),
    '',
    'Either the sources moved, or the extension filter no longer names what they are written in.',
    'Point SOURCE_ROOTS at where they live now.',
  ]);
}

if (conditionCount === 0) {
  vacuous([
    'check-breakpoints: not one media or container query was examined across the source roots.',
    'Every verdict below would be about the empty set, so the scale is not being enforced anywhere.',
    'The condition pattern no longer matches how these queries are written in this tree.',
  ]);
}

const scale = [...bp].map(([n, v]) => `${n} ${v}`).join(', ');

if (findings.length > 0) {
  console.error(
    `check-breakpoints: ${findings.length} media query/queries do not match the declared scale (${scale}):\n`,
  );
  for (const f of findings) {
    console.error(`  ${f.where}\n    (${f.kind}-width: ${f.raw}) — ${f.why}`);
    if (f.hint !== undefined) console.error(`    ${f.hint}`);
  }
  console.error(
    `\nThe scale is declared in ${DECLARATION}. Extend it there if a new breakpoint is genuinely needed.`,
  );
}

if (dangling.length > 0) {
  console.error(
    `\ncheck-breakpoints: ${dangling.length} nf-breakpoint-override annotation(s) exempt nothing:\n`,
  );
  for (const d of dangling) {
    console.error(`  ${d.where}\n    ${d.context}`);
  }
  console.error(
    '\nAn annotation covers its own line and the next one, and only counts as used when it',
  );
  console.error(
    'suppressed a query that would otherwise fail. One that suppresses nothing is either debris',
  );
  console.error(
    'left by a later fix or an annotation the formatter moved off the at-rule it was written for.',
  );
  console.error('Delete it, or put it on its own line directly above the at-rule it belongs to.');
}

if (findings.length > 0 || dangling.length > 0) process.exit(1);

console.info(
  `check-breakpoints: ${conditionCount} media/container queries all match the declared scale (${scale})`,
);
