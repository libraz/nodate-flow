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
// Reachable means invoked, not mentioned. The corpus a candidate is
// looked for in is made of other people's source: Makefile recipes,
// workflow steps, the hook, and the body of every script already known to
// run. A plain substring search over that text counts a filename written
// in a comment — a `#` line in the Makefile, a sentence in another
// script's header explaining what this one does — as evidence that
// something runs it, which is the one direction in which this check
// cannot fail loudly: it reports fewer orphans, and reporting no orphans
// is what a healthy tree looks like. So comments are removed from every
// unit before it enters the corpus, and the surviving text has to look
// like a call: the name preceded by a path separator, or by a runner.
//
// A script invoked in a way that shape cannot see — through a shell
// variable, a loop over a glob — should be exempted with the marker
// below rather than answered by widening the match back to a substring.
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

/**
 * Strip `#` comments from shell, YAML and Makefile recipe text.
 *
 * A `#` only opens a comment at the start of a word and outside quotes.
 * `${var#prefix}` and `${#list[@]}` are parameter expansions, and a `#`
 * inside a quoted string is data — cutting at either would truncate the
 * line and could throw away a real invocation sitting after it.
 */
function stripHashComments(text) {
  const out = [];
  for (const line of text.split('\n')) {
    let quote = null;
    let cut = -1;
    for (let i = 0; i < line.length; i += 1) {
      const c = line[i];
      if (quote !== null) {
        if (c === '\\' && quote === '"') {
          i += 1;
          continue;
        }
        if (c === quote) quote = null;
        continue;
      }
      if (c === "'" || c === '"') {
        quote = c;
        continue;
      }
      if (c === '#' && (i === 0 || /\s/.test(line[i - 1] ?? ''))) {
        cut = i;
        break;
      }
    }
    out.push(cut === -1 ? line : line.slice(0, cut));
  }
  return out.join('\n');
}

/**
 * Strip `//` and block comments from JS, TS and Go source.
 *
 * String and template literals are tracked, so a `//` inside a URL or a
 * quoted path is left alone. This is deliberately not applied to shell
 * or YAML, where `//` is an ordinary part of a path.
 */
function stripSlashComments(text) {
  let out = '';
  let quote = null;
  let i = 0;
  while (i < text.length) {
    const c = text[i];
    const next = text[i + 1];
    if (quote !== null) {
      out += c;
      if (c === '\\') {
        out += next ?? '';
        i += 2;
        continue;
      }
      if (c === quote) quote = null;
      i += 1;
      continue;
    }
    if (c === "'" || c === '"' || c === '`') {
      quote = c;
      out += c;
      i += 1;
      continue;
    }
    if (c === '/' && next === '/') {
      while (i < text.length && text[i] !== '\n') i += 1;
      continue;
    }
    if (c === '/' && next === '*') {
      i += 2;
      while (i < text.length && !(text[i] === '*' && text[i + 1] === '/')) i += 1;
      i += 2;
      continue;
    }
    out += c;
    i += 1;
  }
  return out;
}

/** Comment syntax follows the file's language, not the other way round. */
function stripComments(text, path) {
  return /\.(mjs|js|ts|go)$/.test(path) ? stripSlashComments(text) : stripHashComments(text);
}

/**
 * Tokens that run the file named after them, for the invocations that do
 * not spell out a directory: `node foo.mjs`, `go -C scripts run bar.go`.
 * `run` is included on its own because that is the token both `go run`
 * and `bun run` put directly in front of the name.
 */
const RUNNERS = ['node', 'bun', 'bunx', 'tsx', 'bash', 'sh', 'zsh', 'run', 'python3', 'python'];

/**
 * Does `corpus` call `basename`, as opposed to naming it?
 *
 * Two shapes count: preceded by a path separator (`scripts/x.sh`,
 * `./x.sh`, `"$DIR/x.sh"`), or preceded by a runner token. The trailing
 * boundary keeps `x.sh` from matching inside `x.sh.bak`.
 */
function invokes(corpus, basename) {
  const escaped = basename.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const shape = new RegExp(
    String.raw`(?:/|(?:^|[\s;|&("'\`])(?:${RUNNERS.join('|')})\s+)${escaped}(?![\w.-])`,
  );
  return shape.test(corpus);
}

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

/**
 * Refuse to report success over a set nothing was read into.
 *
 * This check reports the scripts a gate does not reach, so an empty
 * candidate list is its happiest possible output: nothing unreachable,
 * because nothing was looked at. The walk swallows a missing directory,
 * which is precisely how that happens.
 */
function vacuous(lines) {
  for (const line of lines) console.error(line);
  process.exit(2);
}

/** @type {Array<{id: string, body: string, code: string, basename: string}>} */
const scriptCandidates = [];
{
  const roots = [...SCRIPT_ROOTS];
  for (const parent of WORKSPACE_PARENTS) {
    const workspaces = listDirs(parent);
    // A workspace parent that lists nothing takes every per-package
    // scripts/ directory and every workspace package.json out of the
    // inventory at once, and the run still ends in "all reachable".
    if (workspaces.length === 0) {
      vacuous([
        `check-reachability: the workspace parent ${parent}/ holds no directory, so no workspace script or package.json under it entered the inventory.`,
        'Either the workspaces moved, or WORKSPACE_PARENTS names a directory that no longer exists.',
      ]);
    }
    for (const ws of workspaces) roots.push(join(ws, 'scripts'));
  }
  /** Root -> candidates found, so an empty root can be named individually. */
  const countByRoot = new Map();
  const seen = new Set();
  for (const root of roots) {
    const before = scriptCandidates.length;
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
      // Both forms are kept: the marker is a comment, so it is read from
      // the raw body, while only the code contributes to the corpus.
      scriptCandidates.push({
        id,
        body,
        code: stripComments(body, id),
        basename: id.slice(id.lastIndexOf('/') + 1),
      });
    }
    countByRoot.set(root, scriptCandidates.length - before);
  }
  // Only the fixed roots are required to hold candidates. A workspace
  // that has no scripts/ directory of its own is ordinary; `scripts/` and
  // `sql/` going empty is the inventory losing a whole class of guard.
  // Checked one root at a time, because the other root still holding
  // scripts would satisfy any total.
  const emptyRoots = SCRIPT_ROOTS.filter((root) => (countByRoot.get(root) ?? 0) === 0);
  if (emptyRoots.length > 0) {
    vacuous([
      `check-reachability: ${emptyRoots.length} of ${SCRIPT_ROOTS.length} script root(s) yielded no candidate, so nothing under them was tested for being reachable:`,
      ...emptyRoots.map((root) => `  ${root}/`),
      '',
      'Either the scripts moved, or SCRIPT_EXTENSIONS no longer names what they are written in.',
    ]);
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
  // The other half of the inventory. Every workspace manifest could fail
  // to parse and the run would still end in "all reachable".
  if (packageCandidates.length === 0) {
    vacuous([
      'check-reachability: no package.json test or check* script entered the inventory, so that half of the inventory was not tested for being reachable.',
      'Either no manifest could be read, or the scripts are named something isGateScript no longer recognises.',
    ]);
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

/**
 * One recipe command per unit; continuations were joined during parsing.
 * Comments are removed here rather than in the parser, which needs the
 * raw text to find the targets themselves.
 */
function recipeUnits(name) {
  return (targets.get(name)?.recipe ?? []).map(stripHashComments);
}

const closure = new Set();
function addTarget(name) {
  if (!targets.has(name) || closure.has(name)) return;
  closure.add(name);
  for (const prereq of targets.get(name)?.prereqs ?? []) addTarget(prereq);
}
// The gates are the other set that has to be non-empty, and they fail
// the opposite way: a gate that goes missing makes everything look
// unreachable rather than everything look fine. Naming the missing gate
// is still worth more than a list of every script in the tree, which is
// what the report would otherwise be.
for (const gate of ['check', 'test']) {
  if (!targets.has(gate)) {
    vacuous([
      `check-reachability: the Makefile has no \`${gate}\` target, so the reachability closure was never seeded from it.`,
      'Every script that only that gate runs would be reported as reachable by nothing.',
    ]);
  }
  addTarget(gate);
}

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
        workflowUnits.push(stripHashComments(chunk.join('\n')));
        chunk = [];
      }
      chunk.push(line);
    }
    if (chunk.length > 0) workflowUnits.push(stripHashComments(chunk.join('\n')));
  }
}

if (workflowUnits.length === 0) {
  vacuous([
    'check-reachability: .github/workflows holds no workflow file, so CI contributed nothing to the set of gates.',
    'A script that only CI runs would be reported as reachable by nothing.',
  ]);
}

const hookText = (() => {
  try {
    return stripHashComments(readFileSync(join(repo, '.githooks/pre-commit'), 'utf8'));
  } catch {
    return '';
  }
})();

if (hookText === '') {
  vacuous([
    'check-reachability: .githooks/pre-commit could not be read, so the hook contributed nothing to the set of gates.',
    'A script that only the pre-commit hook runs would be reported as reachable by nothing.',
  ]);
}

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
    if (found !== undefined) units.push(...found.code.split('\n'));
  }
  for (const id of reachablePackages) {
    const found = packageCandidates.find((c) => c.id === id);
    if (found !== undefined) units.push(stripHashComments(`${found.dir} ${found.command}`));
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
    if (!invokes(corpus, candidate.basename)) continue;
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
