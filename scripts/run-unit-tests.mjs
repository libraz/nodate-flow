#!/usr/bin/env node
// run-unit-tests — every suite that needs nothing but a compiler.
//
// The repository had three gates and none of them ran a behaviour test.
// `make check` is thirty static guards; the pre-commit hook is lint,
// formatting and codegen drift; only CI ran a suite, and CI runs after the
// commit is already in the branch history. Work here lands straight on the
// working branch, so "CI will catch it" means the branch carries the
// failure until someone reads a notification.
//
// This is the missing rung between the two the Makefile already has:
//
//   make check      static guards — reads the sources, runs no behaviour
//   make test-unit  every suite that needs no container            (here)
//   make test       the above plus the container-backed suites
//
// What defines the tier is what it refuses to start. NF_TEST_INTEGRATION
// is cleared, so the suites that boot MySQL and MinIO through
// testcontainers skip; -short covers the tests that gate on it directly
// instead of on the variable. -race is absent for the same reason. All
// three run unabridged in CI. Nothing here is skipped that is not run
// somewhere else — the point is to move the fast half earlier, not to
// check less.
//
// -short is not a way to hide a guard. The static guards under each
// module's tests/ directory run here as part of ./..., including the
// whole-module type-checking scans, which between them cost more than
// every behaviour test in the repository put together. A guard the fast
// run steps over is one whose verdict nobody sees until CI, so the price
// is paid here rather than deferred.
//
// The Go test cache is deliberately left on: no -count=1. It is keyed on
// the compiled package, the environment a test read and the files it
// opened, so a package whose inputs did not change cannot report a stale
// result, and the run costs nothing for the packages a change did not
// reach. That is what makes --staged cheap enough to sit in a hook, and a
// hook that is not cheap is a hook people learn to route around.
//
// Usage:
//   node scripts/run-unit-tests.mjs            # every module and workspace
//   node scripts/run-unit-tests.mjs --staged   # only what a staged file reaches

import { spawnSync } from 'node:child_process';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');

const stagedOnly = process.argv.includes('--staged');

/** Directories that never hold a module or a workspace of ours. */
const PRUNE = new Set(['node_modules', 'vendor', '.git', 'dist', 'coverage', 'testdata']);

/** Extensions that make a staged file belong to a Go module. */
const GO_FILES = /\.go$/;
/** Extensions that make a staged file belong to a TypeScript workspace. */
const TS_FILES = /\.(ts|tsx|js|jsx|mjs|cjs|json)$/;
/** Filenames that belong to a Go module whatever their extension suggests. */
const GO_MANIFESTS = new Set(['go.mod', 'go.sum']);

// ---------------------------------------------------------------------
// Discovery
// ---------------------------------------------------------------------

/**
 * Every directory under `root` holding a file named `marker`.
 *
 * Derived rather than listed, for the reason the Makefile gives about its
 * own module set: a list reads as exhaustive and stops being true the
 * moment something is added, and a target that visits four of five
 * directories reports green for the four.
 *
 * The `// lint-skip:` marker the Makefile honours is deliberately not read
 * here. It excuses a module from linting, which is a different question
 * from whether its tests should run; a module that must also skip testing
 * has not come up, and inventing a second meaning for that marker now
 * would make both harder to reason about.
 */
function findDirsHolding(root, marker) {
  const found = [];
  const walk = (dir) => {
    let entries;
    try {
      entries = readdirSync(dir, { withFileTypes: true });
    } catch {
      return;
    }
    if (entries.some((e) => e.isFile() && e.name === marker)) found.push(relative(repo, dir));
    for (const entry of entries) {
      if (!entry.isDirectory() || PRUNE.has(entry.name)) continue;
      walk(join(dir, entry.name));
    }
  };
  walk(root);
  return found.filter((id) => id !== '').sort();
}

/** @type {Array<{id: string, kind: 'go'}>} */
const goModules = findDirsHolding(repo, 'go.mod').map((id) => ({ id, kind: 'go' }));

/**
 * TypeScript workspaces, with the name they publish under and whether they
 * have a suite at all. A workspace without one still takes part: it can be
 * the reason a dependent's suite fails, and packages/i18n-shared is
 * exactly that shape today.
 */
/** @type {Array<{id: string, kind: 'ts', name: string, hasSuite: boolean}>} */
const tsWorkspaces = [];
for (const id of findDirsHolding(repo, 'package.json')) {
  if (id === '') continue;
  let manifest;
  try {
    manifest = JSON.parse(readFileSync(join(repo, id, 'package.json'), 'utf8'));
  } catch {
    continue;
  }
  if (typeof manifest.name !== 'string') continue;
  tsWorkspaces.push({
    id,
    kind: 'ts',
    name: manifest.name,
    hasSuite: typeof manifest.scripts?.test === 'string',
  });
}

// Refuse to report success over a set nothing was read into. This runner
// reports the suites that failed, so an empty inventory is its happiest
// possible output: nothing failed, because nothing ran. The walk swallows
// an unreadable directory, which is precisely how that happens — and in
// --staged mode it would look like an ordinary commit that touched no
// code, which is most commits.
const emptySets = [];
if (goModules.length === 0) emptySets.push('no directory holding a go.mod');
if (!tsWorkspaces.some((w) => w.hasSuite))
  emptySets.push('no package.json declaring a test script');
if (emptySets.length > 0) {
  console.error('run-unit-tests: the inventory is empty, so no suite was run:');
  for (const line of emptySets) console.error(`  ${line}`);
  console.error('');
  console.error('Either the modules moved, or the walk no longer reaches them.');
  process.exit(2);
}

// ---------------------------------------------------------------------
// Who breaks when this changes
// ---------------------------------------------------------------------

/**
 * Reverse dependency edges: unit id -> the units that consume it.
 *
 * A change to a shared module is the case where testing only the place
 * the file lives is worst: packages/go-shared holds the auth kit both
 * services build on, and its own suite passing says nothing about the
 * four modules that replace it. Both languages state the edge in a file
 * that is already there, so it is read rather than configured.
 */
/** @type {Map<string, Set<string>>} */
const dependents = new Map();
const addEdge = (from, to) => {
  const set = dependents.get(from);
  if (set === undefined) dependents.set(from, new Set([to]));
  else set.add(to);
};

for (const mod of goModules) {
  let text;
  try {
    text = readFileSync(join(repo, mod.id, 'go.mod'), 'utf8');
  } catch {
    continue;
  }
  // `replace <module> => <path>` is how every in-tree dependency is wired,
  // and the right-hand side is a path relative to the module holding it.
  for (const m of text.matchAll(/^\s*replace\s+\S+\s+=>\s+(\.\S*)/gm)) {
    const target = relative(repo, resolve(repo, mod.id, m[1] ?? ''));
    if (goModules.some((c) => c.id === target)) addEdge(target, mod.id);
  }
}

const workspaceByName = new Map(tsWorkspaces.map((w) => [w.name, w.id]));
for (const ws of tsWorkspaces) {
  let manifest;
  try {
    manifest = JSON.parse(readFileSync(join(repo, ws.id, 'package.json'), 'utf8'));
  } catch {
    continue;
  }
  const deps = { ...manifest.dependencies, ...manifest.devDependencies };
  for (const name of Object.keys(deps)) {
    const target = workspaceByName.get(name);
    if (target !== undefined && target !== ws.id) addEdge(target, ws.id);
  }
}

/** Everything reached from `seeds` by following consumers. */
function withDependents(seeds) {
  const closure = new Set(seeds);
  const queue = [...seeds];
  while (queue.length > 0) {
    const id = queue.pop();
    for (const next of dependents.get(id) ?? []) {
      if (closure.has(next)) continue;
      closure.add(next);
      queue.push(next);
    }
  }
  return closure;
}

// ---------------------------------------------------------------------
// Selection
// ---------------------------------------------------------------------

/** The unit with the longest path that contains `file`, or undefined. */
function enclosing(units, file) {
  let best;
  for (const unit of units) {
    if (!file.startsWith(`${unit.id}/`)) continue;
    if (best === undefined || unit.id.length > best.id.length) best = unit;
  }
  return best;
}

/** @type {Array<{id: string, kind: string}>} */
let selected;

if (stagedOnly) {
  const staged = spawnSync(
    'git',
    ['-C', repo, 'diff', '--cached', '--name-only', '--diff-filter=ACMR'],
    { encoding: 'utf8' },
  );
  if (staged.status !== 0) {
    // Not a repository, or git could not be asked. Saying so beats
    // reporting a clean run over a question that was never put.
    console.error('run-unit-tests: could not read the staged files, so no suite was run.');
    process.exit(2);
  }
  const files = staged.stdout.split('\n').filter(Boolean);
  const seeds = new Set();
  for (const file of files) {
    const base = file.slice(file.lastIndexOf('/') + 1);
    if (GO_FILES.test(file) || GO_MANIFESTS.has(base)) {
      const mod = enclosing(goModules, file);
      if (mod !== undefined) seeds.add(mod.id);
    }
    if (TS_FILES.test(file)) {
      const ws = enclosing(tsWorkspaces, file);
      if (ws !== undefined) seeds.add(ws.id);
    }
  }
  const closure = withDependents(seeds);
  selected = [...goModules, ...tsWorkspaces].filter((u) => closure.has(u.id));
  if (selected.length === 0) process.exit(0);
} else {
  selected = [...goModules, ...tsWorkspaces];
}

// A workspace with no suite can be a seed and can be pulled in as a
// dependency, but there is nothing to run in it.
const runnable = selected.filter((u) => u.kind === 'go' || u.hasSuite === true);
if (runnable.length === 0) process.exit(0);

// ---------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------

// The environment that defines the tier, assigned rather than written as
// an object literal because these are environment variable names and the
// naming rule reads a literal's keys as identifiers.
const goEnv = { ...process.env };
// GOWORK=off resolves each module through its own go.mod, the way the
// Makefile's lint and vet targets do: scripts/ is outside go.work, so
// under the workspace `./...` there matches nothing and the module
// reports a clean sweep of no packages.
goEnv.GOWORK = 'off';
// Empty, not absent: the container-backed suites test for a non-empty
// value and skip on anything else.
goEnv.NF_TEST_INTEGRATION = '';

/**
 * Run every selected suite, and do not stop at the first failure: one red
 * module hiding the next is how a second defect reaches the branch behind
 * the first. The failures are named again at the end, so the result is
 * attributable without reading back through the output.
 */
const failed = [];
for (const unit of runnable) {
  console.info(`==> ${unit.id}`);
  const cwd = join(repo, unit.id);
  const result =
    unit.kind === 'go'
      ? spawnSync('go', ['test', '-short', './...'], { cwd, stdio: 'inherit', env: goEnv })
      : spawnSync('bun', ['run', 'test'], { cwd, stdio: 'inherit' });
  if (result.error !== undefined) {
    console.error(`run-unit-tests: could not run the suite in ${unit.id}: ${result.error.message}`);
    failed.push(unit.id);
    continue;
  }
  if (result.status !== 0) failed.push(unit.id);
}

if (failed.length > 0) {
  console.error('');
  console.error(`run-unit-tests: ${failed.length} suite(s) failed:`);
  for (const id of failed) console.error(`  ${id}`);
  process.exit(1);
}

const scope = stagedOnly ? 'reached by a staged file' : 'in the repository';
console.info(`run-unit-tests: ${runnable.length} suite(s) ${scope} passed`);
