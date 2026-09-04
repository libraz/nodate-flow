#!/usr/bin/env node
// check-reachability — fail when a guard, generator, or test suite exists
// that nothing in this repository ever runs, and when one that `make
// check` runs is run by nothing in CI.
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
//
// The second question has the same shape and the same answer machinery.
// CI does not run `make check`; it runs a hand-picked set of that
// target's prerequisites, one step per guard. So a guard wired into
// `make check` is enforced locally and absent from CI until somebody
// remembers to write it down a second time, in another file, in another
// language — and the pipeline that omits it is green, because a step
// that does not exist reports nothing.
//
// The same fixpoint therefore runs twice more, over the same candidates:
// once seeded from `make check` alone, once from the workflows alone.
// What the first reaches and the second does not is a guard CI does not
// run. A target that genuinely cannot run in CI says so in its own
// recipe, next to the command:
//
//     # ci-local-only: <reason>
//
// which exempts what that target reaches and nothing else.
//
// The self-verification cases run on every invocation, before the
// inventory is built, and a failure among them stops the run. This check
// fails silently in one direction only — it reports fewer orphans — and an
// empty orphan list is what a healthy tree looks like, so a comment
// stripper or an invocation shape that stopped working produces the
// happiest output this script has.

import assert from 'node:assert/strict';
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

/**
 * The exemption marker for the CI half, read from a make target's own
 * recipe so it sits beside the command it excuses. A reason is mandatory
 * for the same reason as above.
 */
const CI_LOCAL_MARKER = /ci-local-only:[^\S\n]*[A-Za-z][^\n]*[A-Za-z]/;

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
 * A token that could be an argument to make: a target name, a path, a
 * variable override. Prose is not, and the difference matters because
 * the corpus is full of sentences that begin "run `make check` and …" —
 * everything after such a phrase is ordinary English, and any word in it
 * that happens to name a target would otherwise enrol that target and
 * everything it runs.
 */
const MAKE_ARGUMENT = /^[A-Za-z0-9._/-]+$/;

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

// ---------------------------------------------------------------------
// Self-verification. Runs before the inventory is built, every time.
// ---------------------------------------------------------------------

function selfCheck() {
  // Names no file in this tree carries, so nothing here can be read as an
  // invocation of a real candidate when this script's own body joins the
  // corpus.
  const guard = 'sample-guard.mjs';
  const task = 'sample-task.ts';

  const cases = [
    [
      'reads a runner call and a path call as invocations',
      () => {
        assert.equal(invokes(`\tnode scripts/${guard}`, guard), true);
        assert.equal(invokes(`\t$(PKG_RUN) scripts/${task}`, task), true);
        assert.equal(invokes(`\tbun run ${task}`, task), true);
      },
    ],
    [
      'does not read a name mentioned in a comment as an invocation',
      () => {
        const mention = `# the nightly job runs scripts/${guard}`;
        assert.equal(invokes(mention, guard), true, 'raw text still contains the shape');
        assert.equal(invokes(stripComments(mention, 'Makefile'), guard), false);
      },
    ],
    [
      'does not match a longer filename that starts with the same name',
      () => {
        assert.equal(invokes(`\tbash scripts/${guard}.bak`, guard), false);
        assert.equal(invokes(`\tbash scripts/${guard} && echo done`, guard), true);
      },
    ],
    [
      'separates a guard CI runs from one it does not, and heeds the local-only marker',
      () => {
        // Two guards, identical in every way except that one workflow
        // step exists. If the two sides of the comparison ever stop
        // being told apart, this is where it shows: the case asserts
        // both directions, so a checker that reports everything and a
        // checker that reports nothing both fail it.
        const paired = 'sample-paired.mjs';
        const lonely = 'sample-lonely.mjs';
        const makefile = [
          'check: guard-paired guard-lonely',
          'guard-paired:',
          `\tnode scripts/${paired}`,
          'guard-lonely:',
          `\tnode scripts/${lonely}`,
          '',
        ].join('\n');
        const targets = parseMakefile(makefile);
        const scripts = [paired, lonely].map((basename) => ({
          id: `scripts/${basename}`,
          body: '',
          code: '',
          basename,
        }));
        const seeds = { targets, scripts, packages: [] };
        const workflow = ['- name: Paired guard', '  run: make guard-paired'].join('\n');

        const fromCheck = reachFrom({ ...seeds, seedTargets: ['check'], seedUnits: [] });
        const fromCI = reachFrom({ ...seeds, seedTargets: [], seedUnits: [workflow] });
        assert.deepEqual([...fromCheck.scripts].sort(), [`scripts/${lonely}`, `scripts/${paired}`]);
        assert.deepEqual([...fromCI.scripts], [`scripts/${paired}`]);

        // The same tree with the marker written on the unpaired target:
        // the guard is still absent from CI, and is no longer reported.
        const declaration = `\t# ci-local-only: needs a device no runner has`;
        const marked = parseMakefile(
          makefile.replace(`\tnode scripts/${lonely}`, `${declaration}\n\tnode scripts/${lonely}`),
        );
        assert.deepEqual([...localOnlyTargets(targets)], []);
        assert.deepEqual([...localOnlyTargets(marked)], ['guard-lonely']);
        const exempted = reachFrom({
          ...seeds,
          targets: marked,
          seedTargets: ['guard-lonely'],
          seedUnits: [],
        });
        assert.deepEqual([...exempted.scripts], [`scripts/${lonely}`]);
      },
    ],
    [
      'tells a package that reads the tree from one that exercises a system',
      () => {
        const guard = [
          'package guard',
          'import (',
          '\t"os"',
          '\t"path/filepath"',
          '\t"testing"',
          ')',
          'func TestX(t *testing.T) { os.ReadDir(filepath.Join("internal")) }',
        ].join('\n');
        const suite = [
          'package suite',
          'import (',
          '\t"database/sql"',
          '\t"path/filepath"',
          '\t"testing"',
          ')',
          'func TestX(t *testing.T) { os.ReadFile(filepath.Join("q.sql")); var _ *sql.DB }',
        ].join('\n');
        // The shape that made the first rule wrong: a service storing
        // nothing has suites that need no database either, and they are
        // still suites, because they do not read the sources.
        const noStorage = [
          'package gateway',
          'import (',
          '\t"net/http/httptest"',
          '\t"testing"',
          ')',
          'func TestX(t *testing.T) { httptest.NewServer(nil) }',
        ].join('\n');

        const classify = (code) =>
          readsTheTree(importPaths(code), code) && !needsInfrastructure(importPaths(code));
        assert.equal(classify(guard), true);
        assert.equal(classify(suite), false);
        assert.equal(classify(noStorage), false);
        assert.deepEqual(importPaths(guard), ['os', 'path/filepath', 'testing']);
      },
    ],
    [
      'reads make arguments as arguments and stops where the sentence starts',
      () => {
        const targets = parseMakefile(['check:', '\ttrue', 'test:', '\ttrue', ''].join('\n'));
        assert.deepEqual(makeTargetsIn(targets, '\t$(MAKE) --no-print-directory check'), ['check']);
        assert.deepEqual(makeTargetsIn(targets, '\tmake -C sub check'), ['check']);
        // The shape every message in this file has: a quoted invocation
        // followed by prose. Only the quoted target is an argument, and
        // it is quoted, so the run ends before the sentence does.
        assert.deepEqual(makeTargetsIn(targets, "'`make check` runs the test suites too'"), []);
      },
    ],
    [
      'reads a go test run out of both shapes, and knows which package sets contain it',
      () => {
        // The guard is a test package, so the only thing that can say CI
        // runs it is the package set of some step's own `go test`.
        const recipe =
          '\tcd apps/flow-api && NF_TEST_INTEGRATION= go test -count=1 ./tests/sample/';
        assert.deepEqual(goTestRuns(recipe), [{ dir: 'apps/flow-api', pkg: './tests/sample' }]);
        const step = [
          '- name: Tests',
          '  working-directory: apps/flow-api',
          '  run: go test ./...',
        ];
        assert.deepEqual(goTestRuns(step.join('\n')), [{ dir: 'apps/flow-api', pkg: './...' }]);

        const want = { dir: 'apps/flow-api', pkg: './tests/sample' };
        assert.equal(goRunCovers({ dir: 'apps/flow-api', pkg: './...' }, want), true);
        assert.equal(goRunCovers({ dir: 'apps/flow-api', pkg: './tests/...' }, want), true);
        assert.equal(goRunCovers({ dir: 'apps/flow-api', pkg: './tests/sample' }, want), true);
        // A sibling module running everything says nothing about this one,
        // and neither does a narrower set in the right module.
        assert.equal(goRunCovers({ dir: 'apps/auth-api', pkg: './...' }, want), false);
        assert.equal(goRunCovers({ dir: 'apps/flow-api', pkg: './internal/...' }, want), false);
      },
    ],
    [
      'strips comments by the language of the file they are in',
      () => {
        assert.equal(
          stripComments(`bash ./${guard} # nightly`, 'run.sh').trim(),
          `bash ./${guard}`,
        );
        const url = `const docs = "https://example.com/${guard}";`;
        assert.equal(stripComments(url, 'a.mjs'), url);
        assert.equal(stripComments(`run(); // node ${guard}`, 'a.mjs').trim(), 'run();');
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
    `check-reachability: ${selfFailures.length} self-verification case(s) failed, so the inventory was not built:\n`,
  );
  for (const f of selfFailures) console.error(f);
  console.error(
    '\nThe scanner itself is wrong. Fix it before trusting anything it says about the sources.',
  );
  process.exit(1);
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
function recipeUnits(targets, name) {
  return (targets.get(name)?.recipe ?? []).map(stripHashComments);
}

/**
 * The make targets whose own recipe declares that CI cannot run them.
 * The raw recipe is read, not the corpus form: the declaration is a
 * comment, and the corpus has its comments removed.
 */
function localOnlyTargets(targets) {
  const marked = new Set();
  for (const [name, target] of targets) {
    if (target.recipe.some((line) => CI_LOCAL_MARKER.test(line))) marked.add(name);
  }
  return marked;
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
}

/** Every make target named in a chunk of text, filtered against real targets. */
function makeTargetsIn(targets, text) {
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
      // The invocation ends where its arguments stop looking like
      // arguments; what follows belongs to the sentence, not to make.
      if (!MAKE_ARGUMENT.test(token)) break;
      if (targets.has(token)) found.push(token);
    }
  }
  return found;
}

// ---------------------------------------------------------------------
// Candidates, second kind: guard packages
// ---------------------------------------------------------------------

/** The import paths a Go source file declares. */
function importPaths(code) {
  const paths = [];
  for (const block of code.matchAll(/import\s*\(([\s\S]*?)\)/g)) {
    for (const quoted of (block[1] ?? '').matchAll(/"([^"]+)"/g)) paths.push(quoted[1] ?? '');
  }
  for (const single of code.matchAll(/import\s+(?:[\w.]+\s+)?"([^"]+)"/g)) {
    paths.push(single[1] ?? '');
  }
  return paths;
}

/**
 * Does this package need a system to run against?
 *
 * Reaching a database, a driver, a container or the tenant helpers means
 * it does. Nothing else can run such a package usefully, so the module's
 * whole-suite sweep is the only sensible caller.
 */
function needsInfrastructure(paths) {
  return paths.some(
    (path) =>
      path === 'database/sql' ||
      path.startsWith('database/sql/') ||
      path.includes('go-sql-driver') ||
      path.includes('testcontainers') ||
      path.endsWith('/tests/helpers'),
  );
}

/**
 * Does this package read the repository's own files?
 *
 * This is what a guard does and a suite does not: it opens the sources
 * and reports on what it finds there, the same job the scans under
 * scripts/ do. Needing no database is not the same question — a service
 * that stores nothing has suites that need none either — so both halves
 * are asked, and a package is a guard only when it reads the tree and
 * needs nothing to run against.
 */
function readsTheTree(paths, code) {
  if (!paths.includes('path/filepath')) return false;
  return /\b(?:filepath\.Walk\w*|fs\.WalkDir|os\.ReadDir|os\.ReadFile|os\.DirFS|os\.Open)\b/.test(
    code,
  );
}

/**
 * Every Go test package under a Go module's tests/ directory, classified.
 *
 * testdata is skipped because the Go tool skips it: the files in there
 * are fixtures, not packages, and several are written to look exactly
 * like the code a guard rejects.
 */
function collectTestPackages() {
  const found = [];
  const visit = (moduleDir, dir) => {
    let entries;
    try {
      entries = readdirSync(dir);
    } catch {
      return;
    }
    const sources = [];
    let isPackage = false;
    for (const entry of entries) {
      const full = join(dir, entry);
      let st;
      try {
        st = statSync(full);
      } catch {
        continue;
      }
      if (st.isDirectory()) {
        if (entry !== 'testdata') visit(moduleDir, full);
        continue;
      }
      if (!entry.endsWith('.go')) continue;
      if (entry.endsWith('_test.go')) isPackage = true;
      sources.push(readFileSync(full, 'utf8'));
    }
    if (!isPackage) return;
    const code = sources.map((text) => stripSlashComments(text)).join('\n');
    const paths = importPaths(code);
    found.push({
      id: relative(repo, dir),
      dir: moduleDir,
      pkg: `./${relative(join(repo, moduleDir), dir)}`,
      guard: readsTheTree(paths, code) && !needsInfrastructure(paths),
      body: sources.join('\n'),
    });
  };
  for (const parent of WORKSPACE_PARENTS) {
    for (const ws of listDirs(parent)) {
      try {
        statSync(join(repo, ws, 'go.mod'));
      } catch {
        continue;
      }
      visit(ws, join(repo, ws, 'tests'));
    }
  }
  return found;
}

const testPackages = collectTestPackages();
const guardPackages = testPackages.filter((p) => p.guard);

// Both classes have to be populated. All-guard means the imports stopped
// being read and every suite is about to be reported; all-suite means the
// classifier swallowed the guards and reports nothing, which is the
// direction that reads like success.
if (guardPackages.length === 0 || guardPackages.length === testPackages.length) {
  vacuous([
    `check-reachability: ${testPackages.length} Go test package(s) under a module's tests/ directory were read, and ${guardPackages.length} of them classified as guards, so the two kinds were not told apart.`,
    'Either the packages moved, or the imports that mark a package as needing a database are no longer the ones it declares.',
  ]);
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

/**
 * The `go test` runs a chunk of text makes, as `{dir, pkg}` pairs.
 *
 * A guard can be a Go test package instead of a script — several are —
 * and those are invisible to the script inventory, which walks scripts/
 * and sql/. The directory comes from the `cd` a recipe starts with or
 * from a workflow step's working-directory, because the package path is
 * relative to it and `./tests/x` under one module is a different package
 * from `./tests/x` under another.
 */
function goTestRuns(unit) {
  const cd = /\bcd\s+([^\s;&|]+)/.exec(unit);
  const wd = /working-directory:\s*(\S+)/.exec(unit);
  const dir = (cd?.[1] ?? wd?.[1] ?? '.').replace(/\/+$/, '');
  const runs = [];
  for (const m of unit.matchAll(/\bgo\s+test\b([^\n;|&]*)/g)) {
    const paths = (m[1] ?? '')
      .trim()
      .split(/\s+/)
      .filter((token) => token.startsWith('.'));
    // `go test` with no package argument tests the directory it runs in.
    if (paths.length === 0) paths.push('.');
    for (const path of paths) runs.push({ dir, pkg: path.replace(/\/+$/, '') });
  }
  return runs;
}

/** Does running `have` also run everything `want` would? */
function goRunCovers(have, want) {
  if (have.dir !== want.dir) return false;
  if (have.pkg === want.pkg) return true;
  if (!have.pkg.endsWith('/...')) return false;
  const prefix = have.pkg.slice(0, -'...'.length);
  return want.pkg === prefix.replace(/\/$/, '') || want.pkg.startsWith(prefix);
}

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

/**
 * Everything a set of seeds reaches: make targets through their
 * prerequisites and their recipes, scripts and package.json scripts
 * through the text of everything already known to run.
 *
 * The seeds and the inventory are parameters rather than the module's
 * own constants because the same fixpoint answers three questions —
 * what does this repository run at all, what does `make check` run, what
 * do the workflows run — and those answers are only comparable when one
 * implementation produces all of them.
 */
function reachFrom({ targets, scripts, packages, seedTargets, seedUnits }) {
  const closure = new Set();
  const addTarget = (name) => {
    if (!targets.has(name) || closure.has(name)) return;
    closure.add(name);
    for (const prereq of targets.get(name)?.prereqs ?? []) addTarget(prereq);
  };
  for (const seed of seedTargets) addTarget(seed);

  const reachedScripts = new Set();
  const reachedPackages = new Set();
  for (let pass = 0; pass < 32; pass += 1) {
    const units = [...seedUnits];
    for (const name of closure) units.push(...recipeUnits(targets, name));
    for (const id of reachedScripts) {
      const found = scripts.find((c) => c.id === id);
      if (found !== undefined) units.push(...found.code.split('\n'));
    }
    for (const id of reachedPackages) {
      const found = packages.find((c) => c.id === id);
      if (found !== undefined) units.push(stripHashComments(`${found.dir} ${found.command}`));
    }
    const corpus = units.join('\n');

    let changed = false;
    for (const target of makeTargetsIn(targets, corpus)) {
      if (!closure.has(target)) {
        addTarget(target);
        changed = true;
      }
    }
    for (const candidate of scripts) {
      if (reachedScripts.has(candidate.id)) continue;
      if (!invokes(corpus, candidate.basename)) continue;
      reachedScripts.add(candidate.id);
      changed = true;
    }
    for (const candidate of packages) {
      if (reachedPackages.has(candidate.id)) continue;
      if (!units.some((unit) => packageUnitMatches(unit, candidate.dir, candidate.name))) continue;
      reachedPackages.add(candidate.id);
      changed = true;
    }
    if (!changed) break;
  }
  return { closure, scripts: reachedScripts, packages: reachedPackages };
}

const inventory = { targets, scripts: scriptCandidates, packages: packageCandidates };

const everything = reachFrom({
  ...inventory,
  seedTargets: ['check', 'test'],
  seedUnits: [...workflowUnits, hookText],
});
const reachableScripts = everything.scripts;
const reachablePackages = everything.packages;

// ---------------------------------------------------------------------
// Report: what nothing runs
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

// ---------------------------------------------------------------------
// Report: what `make check` runs and CI does not
// ---------------------------------------------------------------------

const byCheck = reachFrom({ ...inventory, seedTargets: ['check'], seedUnits: [] });
const byCI = reachFrom({ ...inventory, seedTargets: [], seedUnits: workflowUnits });

const checkReaches = [...byCheck.scripts, ...byCheck.packages];
const ciReaches = new Set([...byCI.scripts, ...byCI.packages]);

// Both sets fail towards silence. An empty `make check` side reports
// perfect parity because there was nothing to compare; an empty CI side
// would report every guard at once, which is loud, but the first is the
// one that reads like success.
if (checkReaches.length === 0) {
  vacuous([
    'check-reachability: `make check` was found to run no script at all, so nothing was compared against CI.',
    'Either the target stopped depending on the guards, or the Makefile parser no longer follows it.',
  ]);
}
if (ciReaches.size === 0) {
  vacuous([
    'check-reachability: the workflows were found to run no script at all, so every guard would be reported as missing from CI.',
    'Either the steps stopped calling make targets, or the workflow parser no longer follows them.',
  ]);
}

// A target that cannot run in CI exempts what it reaches, and only what
// it reaches: the same fixpoint answers that question too, so the
// exemption is as narrow as the target it is written on.
const ciExempt = new Set();
for (const name of localOnlyTargets(targets)) {
  if (!byCheck.closure.has(name)) continue;
  const local = reachFrom({ ...inventory, seedTargets: [name], seedUnits: [] });
  for (const id of local.scripts) ciExempt.add(id);
  for (const id of local.packages) ciExempt.add(id);
}

const ciMissing = checkReaches.filter((id) => !ciReaches.has(id) && !ciExempt.has(id));

// The Go half of the same question. These guards are test packages, so
// the script inventory never sees them, and CI runs them the way it runs
// every other test in the module — through one `./...` — rather than by
// naming the target. What matters is that some workflow step runs a
// package set that contains them.
const ciGoRuns = workflowUnits.flatMap(goTestRuns);
const localOnly = localOnlyTargets(targets);
/** @type {Array<{target: string, run: {dir: string, pkg: string}}>} */
const goRequired = [];
for (const name of byCheck.closure) {
  if (localOnly.has(name)) continue;
  for (const unit of recipeUnits(targets, name)) {
    for (const run of goTestRuns(unit)) goRequired.push({ target: name, run });
  }
}

// Only the CI side is checked for emptiness at runtime. `make check`
// having no Go-backed guard is a legitimate state of the tree, while CI
// running no Go test at all, with guards to cover, means the step shapes
// changed under the parser. The parser itself is proven on every run by
// the self-verification case, which reads both shapes.
if (goRequired.length > 0 && ciGoRuns.length === 0) {
  vacuous([
    'check-reachability: the check target runs Go test packages as guards, and no workflow step was found to run `go test` at all.',
    'Either CI stopped testing Go, or the step no longer has the shape the run parser reads.',
  ]);
}

for (const { target, run } of goRequired) {
  if (ciGoRuns.some((have) => goRunCovers(have, run))) continue;
  ciMissing.push(`${run.dir}: ${run.pkg} (${target})`);
}

if (ciMissing.length > 0) {
  console.error(
    `check-reachability: ${ciMissing.length} guard(s) that \`make check\` runs and no workflow does. These are enforced on the machine of whoever remembers to run them, and nowhere else:`,
  );
  for (const id of ciMissing.sort()) console.error(`  ${id}`);
  console.error('');
  console.error('Add a step to .github/workflows that calls the make target running it, or,');
  console.error('for a guard that is a Go test package, a step whose `go test` covers it.');
  console.error('A target that genuinely cannot run in CI says so in its own recipe:');
  console.error('');
  console.error('  # ci-local-only: <why CI cannot run this>');
  console.error('');
  console.error('written as a tab-indented comment among that recipe’s commands.');
  process.exit(1);
}

// ---------------------------------------------------------------------
// Report: a guard package no gate names
// ---------------------------------------------------------------------

// A guard package is run by the module's whole-suite sweep, so it is
// never unrun — but being swept up is not the same as being a gate, in
// the same way that being named in a comment is not the same as being
// invoked. Only an exact package path counts here: `./...` is the sweep.
const namedByGate = [];
for (const name of byCheck.closure) {
  for (const unit of recipeUnits(targets, name)) namedByGate.push(...goTestRuns(unit));
}

const unnamedGuards = [];
const guardExempt = [];
for (const pkg of guardPackages) {
  if (namedByGate.some((run) => run.dir === pkg.dir && run.pkg === pkg.pkg)) continue;
  if (MARKER.test(pkg.body)) guardExempt.push(pkg.id);
  else unnamedGuards.push(pkg.id);
}

if (unnamedGuards.length > 0) {
  console.error(
    `check-reachability: ${unnamedGuards.length} guard package(s) that no gate names. Each of these reads the tree and reports on it, like the scans under scripts/, but only ever runs inside its module's whole-suite sweep:`,
  );
  for (const id of unnamedGuards.sort()) console.error(`  ${id}`);
  console.error('');
  console.error('Give each one a target that runs that package alone, and put the target in');
  console.error("`check`'s prerequisites, the way the other guard packages are wired. A");
  console.error('package that is deliberately not a gate says so in its own source:');
  console.error('');
  console.error('  unreachable-by-design: <why no gate names this>');
  process.exit(1);
}

console.info(
  `check-reachability: ${guardPackages.length} of ${testPackages.length} Go test package(s) under tests/ are guards, ${guardPackages.length - guardExempt.length} named by a gate`,
);

const ciTotal = checkReaches.length + goRequired.length;
const ciCovered = checkReaches.filter((id) => ciReaches.has(id)).length + goRequired.length;
console.info(
  `check-reachability: ${ciCovered} of ${ciTotal} guard(s) reachable from \`make check\` also run in CI, ${ciTotal - ciCovered} declared local-only`,
);
