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
// Usage:
//   node scripts/check-sdk-browser-safe.mjs

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

const findings = [];

for (const file of walk(sdkSrc, [])) {
  const lines = readFileSync(file, 'utf-8').split('\n');
  lines.forEach((line, i) => {
    // A comment may name these; only a real directive or import counts.
    if (REFERENCE_RE.test(line)) {
      findings.push({ file, line: i + 1, text: line.trim(), why: 'Node type reference directive' });
    } else if (NODE_IMPORT_RE.test(line) && !line.trimStart().startsWith('*')) {
      findings.push({ file, line: i + 1, text: line.trim(), why: 'import of a node: builtin' });
    } else if (TYPES_NODE_RE.test(line) && !line.trimStart().startsWith('*')) {
      findings.push({ file, line: i + 1, text: line.trim(), why: 'reference to @types/node' });
    }
  });
}

// The ban only bites while the compiler is actually configured to withhold
// ambient types; without this the guard would pass on a package that had
// quietly regained them.
const tsconfig = readFileSync(join(sdkRoot, 'tsconfig.json'), 'utf-8');
const hasEmptyTypes = /"types"\s*:\s*\[\s*\]/.test(tsconfig);

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
