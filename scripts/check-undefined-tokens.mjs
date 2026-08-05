#!/usr/bin/env node
// check-undefined-tokens — fail when a design-token reference names a
// custom property nothing defines.
//
// CSS drops any declaration containing an unresolvable `var()` with no
// fallback. Not "falls back to something sensible" — the declaration is
// discarded, as if the line had never been written. So
// `color: var(--nf-color-fg-on-accent)` against an accent background is
// legible text, and `color: var(--nf-color-accent-fg)` — one wrong word,
// no such token — is whatever colour happened to be inherited, on a
// build where every test still passes and every style file still lints.
//
// A reference with a fallback (`var(--x, 1rem)`) still renders, so it is
// reported separately: worth knowing about, not a broken screen.
//
// A definition that reaches itself — `--a: var(--a)`, or the same through
// a longer chain — is checked too. CSS calls that cycle "invalid at
// computed-value time" and the result is identical to naming a token
// nothing defines: every declaration spending it is discarded. The name
// is defined, so the check above sees nothing wrong, and grep sees
// nothing wrong; it takes reading the one line to notice. A tree-wide
// rename that rewrites a token's own definition into a reference to
// itself produces exactly this, silently.
//
// Usage:
//   node scripts/check-undefined-tokens.mjs           # fail on fallback-less refs
//   node scripts/check-undefined-tokens.mjs --strict  # also fail on ones with fallbacks

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');
const strict = process.argv.includes('--strict');

/**
 * Roots that both define and consume tokens. `packages/ui` is where the
 * vocabulary lives; the apps are where it is spent.
 */
const SOURCE_ROOTS = ['packages/ui/src', 'apps/flow-web/src', 'apps/accounts-web/src'];

/** Token namespaces this check owns. */
const TOKEN_RE = /--(?:nf|font)-[A-Za-z0-9_-]+/;
const DEFINITION_RE = /(--(?:nf|font)-[A-Za-z0-9_-]+)\s*:/g;
const REFERENCE_RE = /var\(\s*(--(?:nf|font)-[A-Za-z0-9_-]+)\s*(,)?/g;

function walk(dir, out) {
  let entries;
  try {
    entries = readdirSync(dir);
  } catch {
    return out;
  }
  for (const entry of entries) {
    const full = join(dir, entry);
    // `dist` holds built bundles that inline the same references; they
    // are output, not source, and would double-count every finding.
    if (entry === 'node_modules' || entry === 'dist') continue;
    let s;
    try {
      s = statSync(full);
    } catch {
      continue;
    }
    if (s.isDirectory()) walk(full, out);
    else if (/\.(css|tsx?|mts|cts)$/.test(entry)) out.push(full);
  }
  return out;
}

const files = [];
for (const root of SOURCE_ROOTS) walk(join(repo, root), files);

const defined = new Set();
/** token -> tokens its own value references, for cycle detection. */
const dependsOn = new Map();
/** token -> where it was declared, for the cycle report. */
const declaredAt = new Map();
for (const file of files) {
  const text = readFileSync(file, 'utf8');
  const lines = text.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? '';
    for (const m of line.matchAll(DEFINITION_RE)) {
      const name = m[1];
      if (name === undefined) continue;
      defined.add(name);
      // The value is what follows the colon on this line. Declarations
      // are one per line throughout this codebase; a value spilling onto
      // the next line just means those references are not collected,
      // which can miss a cycle but never invents one.
      const value = line.slice((m.index ?? 0) + m[0].length);
      const deps = dependsOn.get(name) ?? new Set();
      for (const r of value.matchAll(REFERENCE_RE)) {
        if (r[1] !== undefined) deps.add(r[1]);
      }
      dependsOn.set(name, deps);
      if (!declaredAt.has(name)) declaredAt.set(name, `${relative(repo, file)}:${i + 1}`);
    }
  }
}

/** Tokens that reach themselves through their own definitions. */
function findCycles() {
  const cycles = [];
  const state = new Map(); // name -> 'open' | 'done'
  const stack = [];
  const visit = (name) => {
    const seen = state.get(name);
    if (seen === 'done') return;
    if (seen === 'open') {
      cycles.push([...stack.slice(stack.indexOf(name)), name]);
      return;
    }
    state.set(name, 'open');
    stack.push(name);
    for (const dep of dependsOn.get(name) ?? []) visit(dep);
    stack.pop();
    state.set(name, 'done');
  };
  for (const name of dependsOn.keys()) visit(name);
  return cycles;
}
const cycles = findCycles();

/** @type {Map<string, {withFallback: number, bare: Array<{file: string, line: number}>}>} */
const undefinedRefs = new Map();

for (const file of files) {
  const text = readFileSync(file, 'utf8');
  if (!TOKEN_RE.test(text)) continue;
  const lines = text.split('\n');
  for (let i = 0; i < lines.length; i++) {
    for (const m of (lines[i] ?? '').matchAll(REFERENCE_RE)) {
      const name = m[1];
      if (name === undefined || defined.has(name)) continue;
      const entry = undefinedRefs.get(name) ?? { withFallback: 0, bare: [] };
      if (m[2]) entry.withFallback += 1;
      else entry.bare.push({ file: relative(repo, file), line: i + 1 });
      undefinedRefs.set(name, entry);
    }
  }
}

const bareNames = [...undefinedRefs].filter(([, v]) => v.bare.length > 0).sort();
const fallbackOnly = [...undefinedRefs].filter(([, v]) => v.bare.length === 0).sort();

if (bareNames.length > 0) {
  const total = bareNames.reduce((n, [, v]) => n + v.bare.length, 0);
  console.error(
    `check-undefined-tokens: ${total} reference(s) to ${bareNames.length} undefined token(s) with no fallback. Every one of these declarations is discarded by the browser:`,
  );
  for (const [name, v] of bareNames) {
    console.error(`\n  ${name}  (${v.bare.length} declaration(s) dropped)`);
    for (const { file, line } of v.bare.slice(0, 8)) console.error(`    ${file}:${line}`);
    if (v.bare.length > 8) console.error(`    ... and ${v.bare.length - 8} more`);
  }
}

if (fallbackOnly.length > 0) {
  const label = strict ? 'error' : 'note';
  const log = strict ? console.error : console.info;
  log(
    `\ncheck-undefined-tokens (${label}): ${fallbackOnly.length} undefined token(s) referenced only with a fallback. These still render, but the token they name does not exist:`,
  );
  for (const [name, v] of fallbackOnly) log(`  ${name}  (${v.withFallback} reference(s))`);
}

if (cycles.length > 0) {
  console.error(
    `\ncheck-undefined-tokens: ${cycles.length} token(s) whose definition reaches itself. CSS treats a cycle as invalid at computed-value time, so every declaration spending them is discarded — the same visible result as an undefined token:`,
  );
  for (const cycle of cycles) {
    console.error(`\n  ${cycle.join(' -> ')}`);
    console.error(`    declared at ${declaredAt.get(cycle[0]) ?? 'unknown'}`);
  }
}

if (bareNames.length > 0 || cycles.length > 0 || (strict && fallbackOnly.length > 0)) {
  console.error(
    '\nEither define the token in packages/ui, or point the reference at the token that already means this.',
  );
  process.exit(1);
}

console.info(`check-undefined-tokens: ${defined.size} tokens defined, all references resolve`);
