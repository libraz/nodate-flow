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
// The self-verification cases run on every invocation, before the real
// scan, and a failure among them stops the run. Reading files proves the
// roots are there; it does not prove the three patterns below still
// recognise a definition, a reference, or a fallback. Each of those
// failing silently turns the whole check into a statement about the empty
// set, which reads as success.
//
// Usage:
//   node scripts/check-undefined-tokens.mjs           # fail on fallback-less refs
//   node scripts/check-undefined-tokens.mjs --strict  # also fail on ones with fallbacks

import assert from 'node:assert/strict';
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

/**
 * Refuse to report success over a set nothing was read into.
 *
 * A scan root that has been renamed produces no files, no references and
 * no findings, which is indistinguishable from a clean tree. Every set
 * this check reasons over is proved non-empty before its verdict is
 * allowed to mean anything, and the roots are proved one at a time: a
 * total stays satisfied by the roots that still exist while the renamed
 * one quietly stops being checked.
 */
function vacuous(lines) {
  for (const line of lines) console.error(line);
  process.exit(2);
}

/**
 * Token definitions in one source: name -> the tokens its own value
 * spends, and the 1-based line it was first declared on.
 *
 * The value is what follows the colon on that line. Declarations are one
 * per line throughout this codebase; a value spilling onto the next line
 * just means those references are not collected, which can miss a cycle
 * but never invents one.
 */
function definitionsIn(text) {
  const out = new Map();
  const lines = text.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? '';
    for (const m of line.matchAll(DEFINITION_RE)) {
      const name = m[1];
      if (name === undefined) continue;
      const existing = out.get(name);
      const deps = existing?.deps ?? new Set();
      const value = line.slice((m.index ?? 0) + m[0].length);
      for (const r of value.matchAll(REFERENCE_RE)) {
        if (r[1] !== undefined) deps.add(r[1]);
      }
      out.set(name, { deps, line: existing?.line ?? i + 1 });
    }
  }
  return out;
}

/** Every `var(--nf-*)` reference in one source, with its 1-based line. */
function referencesIn(text) {
  const out = [];
  const lines = text.split('\n');
  for (let i = 0; i < lines.length; i++) {
    for (const m of (lines[i] ?? '').matchAll(REFERENCE_RE)) {
      const name = m[1];
      if (name === undefined) continue;
      out.push({ name, line: i + 1, hasFallback: m[2] !== undefined });
    }
  }
  return out;
}

/** Tokens that reach themselves through their own definitions. */
function findCycles(dependsOn) {
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

// ---------------------------------------------------------------------------
// Self-verification. Runs before the scan, every time.
// ---------------------------------------------------------------------------

function selfCheck() {
  const cases = [
    [
      'tells a fallback-less reference from one that carries a fallback',
      () => {
        assert.deepEqual(referencesIn('  color: var(--nf-color-gone);'), [
          { name: '--nf-color-gone', line: 1, hasFallback: false },
        ]);
        assert.deepEqual(referencesIn('  gap: var(--nf-space-gone, 1rem);'), [
          { name: '--nf-space-gone', line: 1, hasFallback: true },
        ]);
      },
    ],
    [
      'reads a declaration as a definition and a use as neither',
      () => {
        assert.deepEqual([...definitionsIn('  --nf-color-fg: #111;').keys()], ['--nf-color-fg']);
        assert.equal(definitionsIn('  color: var(--nf-color-fg);').size, 0);
      },
    ],
    [
      'collects the tokens a definition spends in its own value',
      () => {
        const defs = definitionsIn('  --nf-color-fg: var(--nf-color-ink);');
        assert.deepEqual([...(defs.get('--nf-color-fg')?.deps ?? [])], ['--nf-color-ink']);
      },
    ],
    [
      'finds a definition that reaches itself and leaves an acyclic chain alone',
      () => {
        assert.deepEqual(findCycles(new Map([['--nf-a', new Set(['--nf-a'])]])), [
          ['--nf-a', '--nf-a'],
        ]);
        assert.deepEqual(
          findCycles(
            new Map([
              ['--nf-a', new Set(['--nf-b'])],
              ['--nf-b', new Set()],
            ]),
          ),
          [],
        );
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
    `check-undefined-tokens: ${selfFailures.length} self-verification case(s) failed, so the scan was not run:\n`,
  );
  for (const f of selfFailures) console.error(f);
  console.error(
    '\nThe scanner itself is wrong. Fix it before trusting anything it says about the sources.',
  );
  process.exit(1);
}

// ---------------------------------------------------------------------------
// The scan.
// ---------------------------------------------------------------------------

const files = [];
/** Root -> files walked, so an empty root can be named rather than summed away. */
const filesByRoot = new Map();
for (const root of SOURCE_ROOTS) {
  const before = files.length;
  walk(join(repo, root), files);
  filesByRoot.set(root, files.length - before);
}

const emptyRoots = [...filesByRoot].filter(([, n]) => n === 0).map(([root]) => root);
if (emptyRoots.length > 0) {
  vacuous([
    `check-undefined-tokens: ${emptyRoots.length} of ${SOURCE_ROOTS.length} scan root(s) hold no source file, so nothing under them was checked:`,
    ...emptyRoots.map((root) => `  ${root}`),
    '',
    'Either the sources moved, or the extension filter no longer names what they are written in.',
    'Point SOURCE_ROOTS at where they live now.',
  ]);
}

const defined = new Set();
/** token -> tokens its own value references, for cycle detection. */
const dependsOn = new Map();
/** token -> where it was declared, for the cycle report. */
const declaredAt = new Map();
for (const file of files) {
  for (const [name, { deps, line }] of definitionsIn(readFileSync(file, 'utf8'))) {
    defined.add(name);
    const merged = dependsOn.get(name) ?? new Set();
    for (const dep of deps) merged.add(dep);
    dependsOn.set(name, merged);
    if (!declaredAt.has(name)) declaredAt.set(name, `${relative(repo, file)}:${line}`);
  }
}

// The definitions are the second set with a vacuous mode of its own. An
// empty one does not hide undefined references — with nothing defined,
// every reference is reported and the check fails loudly — but the cycle
// pass reads only this map, so with no definitions collected there is
// nothing to walk and no cycle can be found however many exist.
if (defined.size === 0) {
  vacuous([
    `check-undefined-tokens: ${files.length} file(s) were read and not one token definition was found.`,
    'No definition means no dependency graph, so the cycle pass walked nothing and proves nothing.',
    'The definition pattern no longer matches how tokens are declared in this tree.',
  ]);
}

const cycles = findCycles(dependsOn);

/** @type {Map<string, {withFallback: number, bare: Array<{file: string, line: number}>}>} */
const undefinedRefs = new Map();
/** References examined, including the ones that resolve. */
let referenceCount = 0;

for (const file of files) {
  const text = readFileSync(file, 'utf8');
  if (!TOKEN_RE.test(text)) continue;
  for (const { name, line, hasFallback } of referencesIn(text)) {
    referenceCount += 1;
    if (defined.has(name)) continue;
    const entry = undefinedRefs.get(name) ?? { withFallback: 0, bare: [] };
    if (hasFallback) entry.withFallback += 1;
    else entry.bare.push({ file: relative(repo, file), line });
    undefinedRefs.set(name, entry);
  }
}

// With no reference collected there is nothing to resolve, and "all
// references resolve" is a statement about the empty set.
if (referenceCount === 0) {
  vacuous([
    `check-undefined-tokens: ${files.length} file(s) were read and not one var(--nf-*) reference was found.`,
    'Nothing was resolved against the defined tokens, so the check confirmed nothing.',
    'The reference pattern no longer matches how tokens are spent in this tree.',
  ]);
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

console.info(
  `check-undefined-tokens: ${defined.size} tokens defined, ${referenceCount} references across ${files.length} files, all resolve`,
);
