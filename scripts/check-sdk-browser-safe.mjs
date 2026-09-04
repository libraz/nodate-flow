#!/usr/bin/env node
// check-sdk-browser-safe — keep Node types out of the browser SDK.
//
// `packages/sdk` is bundled into both web apps, so nothing it ships may
// reach for a Node global. `refresh.ts` carries a hand-written base64
// decoder for exactly this reason: it cannot use `atob` (absent outside
// browsers) or `Buffer` (absent in them).
//
// `packages/sdk/tsconfig.json` sets `"types": []`, which makes `Buffer` and
// `process` fail to compile. That setting alone is not enough, because two
// things silently switch it back on for the entire project — not just for
// the file that does it:
//
//   - `/// <reference types="node" />` anywhere in the project. This is
//     what happened once already: a test needed `node:fs` to read a Go
//     source file, added the directive, and from then on shipped code could
//     have called `Buffer.from` with nothing objecting.
//   - importing a `node:*` builtin, which drags the same types in.
//
// Both are invisible in review — the diff is one line in a test file, and
// no check turns red. So this guard bans the two doors rather than trying
// to catch every Node global that walks through them; with the doors shut,
// tsc itself rejects the globals.
//
// A cross-language comparison that genuinely needs to read files belongs in
// this directory instead. `check-region-parity.mjs` is the worked example.
//
// The self-verification cases run on every invocation, before the walk,
// and a failure among them stops the run. Both doors are recognised by a
// pattern, and a pattern that stopped matching reports a clean package
// exactly the way a genuinely clean one does.
//
// Usage:
//   node scripts/check-sdk-browser-safe.mjs

import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');
const sdkRoot = join(repo, 'packages/sdk');
const sdkSrc = join(sdkRoot, 'src');

/** `/// <reference types="node" />`, in any spacing. */
const REFERENCE_RE = /\/\/\/\s*<reference\s+types\s*=\s*["']node["']\s*\/>/;
/** `from 'node:fs'`, `import 'node:fs'`, `require('node:fs')`, `import('node:fs')`. */
const NODE_IMPORT_RE = /(?:from|import|require)\s*\(?\s*["']node:[^"']+["']/;
/** A bare `@types/node` mention in a tsconfig-ish or triple-slash position. */
const TYPES_NODE_RE = /["']@types\/node["']/;

function walk(dir, out) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      walk(full, out);
    } else if (/\.(?:ts|tsx|mts|cts)$/.test(entry)) {
      out.push(full);
    }
  }
  return out;
}

/**
 * Which door this line opens, or null when it opens none.
 *
 * A comment may name these; only a real directive or import counts, so a
 * continuation line of a doc comment is excused. The directive itself is
 * written as a comment and cannot be.
 */
function doorOpenedBy(line) {
  if (REFERENCE_RE.test(line)) return 'Node type reference directive';
  const inDocComment = line.trimStart().startsWith('*');
  if (NODE_IMPORT_RE.test(line) && !inDocComment) return 'import of a node: builtin';
  if (TYPES_NODE_RE.test(line) && !inDocComment) return 'reference to @types/node';
  return null;
}

/** Does this tsconfig still withhold ambient types from the project? */
function withholdsAmbientTypes(tsconfig) {
  return /"types"\s*:\s*\[\s*\]/.test(tsconfig);
}

// ---------------------------------------------------------------------------
// Self-verification. Runs before the walk, every time.
// ---------------------------------------------------------------------------

function selfCheck() {
  const cases = [
    [
      'reports the type reference directive',
      () => {
        assert.equal(
          doorOpenedBy('/// <reference types="node" />'),
          'Node type reference directive',
        );
        assert.equal(doorOpenedBy("///<reference types='node'/>"), 'Node type reference directive');
      },
    ],
    [
      'reports an import of a node: builtin, in any of its forms',
      () => {
        assert.equal(
          doorOpenedBy("import { readFileSync } from 'node:fs';"),
          'import of a node: builtin',
        );
        assert.equal(doorOpenedBy("const p = require('node:path');"), 'import of a node: builtin');
        assert.equal(doorOpenedBy('  "types": ["@types/node"]'), 'reference to @types/node');
      },
    ],
    [
      'leaves an ordinary import and a doc comment naming the same thing alone',
      () => {
        assert.equal(doorOpenedBy("import { decodeBase64 } from './base64';"), null);
        assert.equal(doorOpenedBy(' * a check that needs node:fs belongs in scripts/'), null);
        assert.equal(doorOpenedBy("import { toRefreshToken } from './refresh';"), null);
      },
    ],
    [
      'tells an empty types array from one that names a package',
      () => {
        assert.equal(withholdsAmbientTypes('{ "compilerOptions": { "types": [] } }'), true);
        assert.equal(withholdsAmbientTypes('{ "compilerOptions": { "types": ["node"] } }'), false);
        assert.equal(withholdsAmbientTypes('{ "compilerOptions": { "strict": true } }'), false);
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
    `check-sdk-browser-safe: ${selfFailures.length} self-verification case(s) failed, so the scan was not run:\n`,
  );
  for (const f of selfFailures) console.error(f);
  console.error(
    '\nThe scanner itself is wrong. Fix it before trusting anything it says about the package.',
  );
  process.exit(1);
}

// ---------------------------------------------------------------------------
// The scan.
// ---------------------------------------------------------------------------

const findings = [];

for (const file of walk(sdkSrc, [])) {
  const lines = readFileSync(file, 'utf-8').split('\n');
  lines.forEach((line, i) => {
    const why = doorOpenedBy(line);
    if (why !== null) findings.push({ file, line: i + 1, text: line.trim(), why });
  });
}

// The ban only bites while the compiler is actually configured to withhold
// ambient types; without this the guard would pass on a package that had
// quietly regained them.
const tsconfig = readFileSync(join(sdkRoot, 'tsconfig.json'), 'utf-8');
const hasEmptyTypes = withholdsAmbientTypes(tsconfig);

if (!hasEmptyTypes) {
  console.error(
    'check-sdk-browser-safe: packages/sdk/tsconfig.json no longer sets "types": [], so whether shipped code can call Buffer depends on where the package manager hoisted @types/node.',
  );
}

if (findings.length > 0) {
  console.error(
    `\ncheck-sdk-browser-safe: ${findings.length} place(s) pull Node types into the browser SDK:`,
  );
  for (const f of findings) {
    console.error(`  ${relative(repo, f.file)}:${f.line}  ${f.why}`);
    console.error(`    ${f.text}`);
  }
  console.error(
    '\nThese apply to the whole project, not just the file they appear in — shipped modules can reach for Buffer / process as soon as one of them lands.',
  );
  console.error(
    'A check that has to read files belongs in scripts/ instead; see scripts/check-region-parity.mjs.',
  );
}

if (findings.length > 0 || !hasEmptyTypes) {
  process.exit(1);
}

console.info('check-sdk-browser-safe: the browser SDK carries no Node types');
