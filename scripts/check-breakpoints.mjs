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
  const src = readFileSync(join(repo, DECLARATION), 'utf8');
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

const bp = readBreakpoints();
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

/** Lines an annotation exempts: its own, and the one after it. */
function exemptLines(lines) {
  const out = new Set();
  for (let i = 0; i < lines.length; i++) {
    if (OVERRIDE.test(lines[i] ?? '')) {
      out.add(i + 1);
      out.add(i + 2);
    }
  }
  return out;
}

for (const root of SOURCE_ROOTS) {
  for (const file of walk(join(repo, root), [])) {
    const text = readFileSync(file, 'utf8');
    if (!text.includes('-width:')) continue;
    const lines = text.split('\n');
    const exempt = exemptLines(lines);
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i] ?? '';
      // Skip prose that quotes a query rather than writing one.
      if (/^\s*(\*|\/\/)/.test(line)) continue;
      if (exempt.has(i + 1)) continue;
      for (const m of line.matchAll(CONDITION)) {
        const kind = m[1];
        const raw = m[2] ?? '';
        const where = `${relative(repo, file)}:${i + 1}`;
        const px = /^(\d+(?:\.\d+)?)px$/.test(raw)
          ? Number(raw.replace('px', ''))
          : /^(\d+(?:\.\d+)?)rem$/.test(raw)
            ? Number(raw.replace('rem', '')) * 16
            : null;
        if (px === null) {
          findings.push({ where, raw, kind, why: 'not a plain px/rem length' });
          continue;
        }
        if (!raw.endsWith('px')) {
          findings.push({
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
          findings.push({
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
    }
  }
}

if (findings.length > 0) {
  console.error(
    `check-breakpoints: ${findings.length} media query/queries do not match the declared scale (${[
      ...bp,
    ]
      .map(([n, v]) => `${n} ${v}`)
      .join(', ')}):\n`,
  );
  for (const f of findings) {
    console.error(`  ${f.where}\n    (${f.kind}-width: ${f.raw}) — ${f.why}`);
  }
  console.error(
    `\nThe scale is declared in ${DECLARATION}. Extend it there if a new breakpoint is genuinely needed.`,
  );
  process.exit(1);
}

console.info(
  `check-breakpoints: every media query matches the declared scale (${[...bp].map(([n, v]) => `${n} ${v}`).join(', ')})`,
);
