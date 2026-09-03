#!/usr/bin/env node
// check-reachability — fail when a guard, generator, or test suite exists
// that nothing in this repository ever runs.
//
// A check nobody invokes is indistinguishable from a check that passes.
// The repository accumulated several: scans written for a real failure
// mode, committed, and then never wired to anything, so the invariant
// they describe went unenforced for months while every pipeline stayed
// green. Nothing could notice, because "is this script called from
// somewhere" was a question only a human reading the tree could ask.
//
// This asks it mechanically. The inventory is derived, never listed:
//
//   Candidates  every script under scripts/, sql/, and each workspace's
//               own scripts/ directory, plus every package.json script
//               named `test` or `check*`.
//   Gates       `make check`, `make test`, the GitHub Actions workflows,
//               and .githooks/pre-commit — expanded transitively, so a
//               target reached through another target, or a script
//               invoked by an already-reachable script, counts.
//
// A candidate that no gate reaches is reported. The way out is to wire
// it or to delete it; a script that is deliberately neither — a manual
// instrument, a one-off seeding helper — declares that in its own body:
//
//     unreachable-by-design: <reason>
//
// The declaration lives with the script rather than in a list here, so
// it cannot outlive the file it exempts, and the reason is read by
// whoever next opens the script. package.json has no comments, so a
// script exempted there carries the same marker as a trailing shell
// comment in its command.

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');

/**
 * The exemption marker. A reason is mandatory and has to read as prose:
 * requiring the text after the colon to start and end with a letter is
 * what stops documentation of the marker from acting as an invocation
 * of it — the same rule scripts/lib/token-override.mjs uses.
 */
const MARKER = /unreachable-by-design:[^\S\n]*[A-Za-z][^\n]*[A-Za-z]/;

/** Directories whose scripts are candidates. */
const SCRIPT_ROOTS = ['scripts', 'sql'];
/** Workspace parents that may hold a per-package scripts/ directory. */
const WORKSPACE_PARENTS = ['apps', 'packages'];

const SCRIPT_EXTENSIONS = ['.sh', '.mjs', '.js', '.ts', '.go'];
const SKIP_DIRS = new Set(['node_modules', 'dist', '.git', 'coverage', 'locales']);

/** package.json script names that are gates rather than utilities. */
const isGateScript = (name) => name === 'test' || name.startsWith('check');

function walk(dir, out) {
  let entries;
  try {
    entries = readdirSync(dir);
  } catch {
    return out;
  }
  for (const entry of entries) {
    if (SKIP_DIRS.has(entry)) continue;
    const full = join(dir, entry);
    let st;
    try {
      st = statSync(full);
    } catch {
      continue;
    }
    if (st.isDirectory()) {
      walk(full, out);
      continue;
    }
    if (!st.isFile()) continue;
    if (entry.endsWith('.d.ts')) continue;
    if (entry.includes('.test.') || entry.includes('.spec.')) continue;
    if (!SCRIPT_EXTENSIONS.some((ext) => entry.endsWith(ext))) continue;
    out.push(full);
  }
  return out;
}

function listDirs(parent) {
  try {
    return readdirSync(join(repo, parent))
      .map((name) => join(parent, name))
      .filter((rel) => {
        try {
          return statSync(join(repo, rel)).isDirectory();
        } catch {
          return false;
        }
      });
  } catch {
    return [];
  }
}

// ---------------------------------------------------------------------
// Candidates
// ---------------------------------------------------------------------

/** @type {Array<{id: string, body: string, basename: string}>} */
const scriptCandidates = [];
{
  const roots = [...SCRIPT_ROOTS];
  for (const parent of WORKSPACE_PARENTS) {
    for (const ws of listDirs(parent)) roots.push(join(ws, 'scripts'));
  }
  const seen = new Set();
  for (const root of roots) {
    for (const file of walk(join(repo, root), [])) {
      const id = relative(repo, file);
      if (seen.has(id)) continue;
      seen.add(id);
      let body = '';
      try {
        body = readFileSync(file, 'utf8');
      } catch {
        body = '';
      }
      scriptCandidates.push({ id, body, basename: id.slice(id.lastIndexOf('/') + 1) });
    }
  }
}

/** @type {Array<{id: string, dir: string, name: string, command: string}>} */
const packageCandidates = [];
{
  const dirs = ['.'];
  for (const parent of WORKSPACE_PARENTS) dirs.push(...listDirs(parent));
  for (const dir of dirs) {
    let manifest;
    try {
      manifest = JSON.parse(readFileSync(join(repo, dir, 'package.json'), 'utf8'));
    } catch {
      continue;
    }
    for (const [name, command] of Object.entries(manifest.scripts ?? {})) {
      if (!isGateScript(name)) continue;
      packageCandidates.push({
        id: `${dir === '.' ? 'package.json' : `${dir}/package.json`} -> ${name}`,
        dir,
        name,
        command: String(command),
      });
    }
  }
}

// ---------------------------------------------------------------------
// Gates
// ---------------------------------------------------------------------

/**
 * Parse the Makefile into targets. Only prerequisites and recipe text
 * are used; variable assignments and the conditionals around them are
 * not targets and are skipped.
 */
function parseMakefile(text) {
  /** @type {Map<string, {prereqs: string[], recipe: string[]}>} */
  const parsed = new Map();
  let current = [];
  // Join backslash continuations first, so one logical line is one
  // string. Both halves of the file need it: a target whose
  // prerequisites wrap would otherwise contribute only the ones on its
  // first line, and every target listed after the wrap would look
  // unreachable while `make test` ran it every time.
  const logical = [];
  for (const line of text.split('\n')) {
    const previous = logical.length > 0 ? logical[logical.length - 1] : undefined;
    if (previous !== undefined && /\\\s*$/.test(previous)) {
      logical[logical.length - 1] = previous.replace(/\\\s*$/, ' ') + line.replace(/^[ \t]+/, '');
      continue;
    }
    logical.push(line);
  }
  for (const raw of logical) {
    if (raw.startsWith('\t')) {
      for (const name of current) parsed.get(name)?.recipe.push(raw.slice(1));
      continue;
    }
    const trimmed = raw.trim();
    if (trimmed === '' || trimmed.startsWith('#')) continue;
    const m = /^([^\t#=:][^=:]*):(?!=)\s*(.*)$/.exec(raw);
    if (m === null) {
      current = [];
      continue;
    }
    const names = (m[1] ?? '').trim().split(/\s+/).filter(Boolean);
    // `.PHONY` and its relatives list every target in the file; reading
    // their right-hand side as prerequisites would make everything
    // reachable from anything.
    if (names.some((n) => n.startsWith('.'))) {
      current = [];
      continue;
    }
    const prereqs = (m[2] ?? '').split('##')[0].trim().split(/\s+/).filter(Boolean);
    current = names;
    for (const name of names) {
      const existing = parsed.get(name);
      if (existing === undefined) parsed.set(name, { prereqs, recipe: [] });
      else existing.prereqs.push(...prereqs);
    }
  }
  return parsed;
}

const targets = parseMakefile(readFileSync(join(repo, 'Makefile'), 'utf8'));

/** One recipe command per unit; continuations were joined during parsing. */
function recipeUnits(name) {
  return targets.get(name)?.recipe ?? [];
}

const closure = new Set();
function addTarget(name) {
  if (!targets.has(name) || closure.has(name)) return;
  closure.add(name);
  for (const prereq of targets.get(name)?.prereqs ?? []) addTarget(prereq);
}
addTarget('check');
addTarget('test');

/** Every make target named in a chunk of text, filtered against real targets. */
function makeTargetsIn(text) {
  const found = [];
  for (const m of text.matchAll(/(?:\$\(MAKE\)|\bmake\b)([^\n;|&]*)/g)) {
    const tokens = (m[1] ?? '').trim().split(/\s+/);
    for (let i = 0; i < tokens.length; i += 1) {
      const token = tokens[i] ?? '';
      if (token === '-C' || token === '-f') {
        i += 1;
        continue;
      }
      if (token.startsWith('-') || token.includes('=') || token.includes('$')) continue;
      if (targets.has(token)) found.push(token);
    }
  }
  return found;
}

/**
 * Workflow files split into per-step chunks, so a step's
 * working-directory and its command stay in the same unit.
 */
const workflowUnits = [];
{
  const dir = join(repo, '.github/workflows');
  let entries = [];
  try {
    entries = readdirSync(dir).filter((n) => n.endsWith('.yml') || n.endsWith('.yaml'));
  } catch {
    entries = [];
  }
  for (const entry of entries) {
    const text = readFileSync(join(dir, entry), 'utf8');
    let chunk = [];
    for (const line of text.split('\n')) {
      if (/^\s*-\s/.test(line) && chunk.length > 0) {
        workflowUnits.push(chunk.join('\n'));
        chunk = [];
      }
      chunk.push(line);
    }
    if (chunk.length > 0) workflowUnits.push(chunk.join('\n'));
  }
}

const hookText = (() => {
  try {
    return readFileSync(join(repo, '.githooks/pre-commit'), 'utf8');
  } catch {
    return '';
  }
})();

// ---------------------------------------------------------------------
// Fixpoint: a gate reaches a script, whose body reaches the next one
// ---------------------------------------------------------------------

const reachableScripts = new Set();
const reachablePackages = new Set();

function packageUnitMatches(unit, dir, name) {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  if (!new RegExp(`(?:bun run|\\$\\(PKG_RUN\\))\\s+${escaped}(?![A-Za-z0-9_:.-])`).test(unit)) {
    return false;
  }
  if (dir !== '.') return unit.includes(dir);
  // The root package's scripts only count when the command runs at the
  // root: the same name inside a workspace is a different script.
  return !/\bcd\s/.test(unit) && !/working-directory:/.test(unit);
}

for (let pass = 0; pass < 32; pass += 1) {
  const units = [...workflowUnits, hookText];
  for (const name of closure) units.push(...recipeUnits(name));
  for (const id of reachableScripts) {
    const found = scriptCandidates.find((c) => c.id === id);
    if (found !== undefined) units.push(...found.body.split('\n'));
  }
  for (const id of reachablePackages) {
    const found = packageCandidates.find((c) => c.id === id);
    if (found !== undefined) units.push(`${found.dir} ${found.command}`);
  }
  const corpus = units.join('\n');

  let changed = false;
  for (const target of makeTargetsIn(corpus)) {
    if (!closure.has(target)) {
      addTarget(target);
      changed = true;
    }
  }
  for (const candidate of scriptCandidates) {
    if (reachableScripts.has(candidate.id)) continue;
    if (!corpus.includes(candidate.basename)) continue;
    reachableScripts.add(candidate.id);
    changed = true;
  }
  for (const candidate of packageCandidates) {
    if (reachablePackages.has(candidate.id)) continue;
    if (!units.some((unit) => packageUnitMatches(unit, candidate.dir, candidate.name))) continue;
    reachablePackages.add(candidate.id);
    changed = true;
  }
  if (!changed) break;
}

// ---------------------------------------------------------------------
// Report
// ---------------------------------------------------------------------

const orphans = [];
const exempt = [];
for (const candidate of scriptCandidates) {
  if (reachableScripts.has(candidate.id)) continue;
  if (MARKER.test(candidate.body)) exempt.push(candidate.id);
  else orphans.push(candidate.id);
}
for (const candidate of packageCandidates) {
  if (reachablePackages.has(candidate.id)) continue;
  if (MARKER.test(candidate.command)) exempt.push(candidate.id);
  else orphans.push(candidate.id);
}

if (orphans.length > 0) {
  console.error(
    `check-reachability: ${orphans.length} script(s) that no gate runs. Nothing invokes these, so whatever they check is not checked:`,
  );
  for (const id of orphans.sort()) console.error(`  ${id}`);
  console.error('');
  console.error('Wire each one into `make check` (static guards) or `make test` (suites), or');
  console.error('delete it. A script that is deliberately neither says so in its own body:');
  console.error('');
  console.error('  unreachable-by-design: <why nothing runs this>');
  console.error('');
  console.error('written as a comment in the script, or as a trailing `#` comment on the');
  console.error('command of a package.json script.');
  process.exit(1);
}

const total = scriptCandidates.length + packageCandidates.length;
console.info(
  `check-reachability: ${total - exempt.length} of ${total} scripts reachable from a gate, ${exempt.length} declared unreachable by design`,
);
