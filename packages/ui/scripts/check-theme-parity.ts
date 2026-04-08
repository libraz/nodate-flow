/**
 * check-theme-parity
 *
 * Asserts that every CSS custom property declared in src/tokens/semantic.css
 * is also declared in each of the 4 theme files. Exits non-zero on any miss.
 *
 * Intentionally avoids regular expressions: we walk the file character by character
 * looking for `--nf-` identifiers preceded by start-of-line / whitespace and
 * followed by `:`.
 */

import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, '..');

const semanticPath = resolve(root, 'src/tokens/semantic.css');
const themePaths = [
  resolve(root, 'src/themes/aurora-light.css'),
  resolve(root, 'src/themes/aurora-dark.css'),
  resolve(root, 'src/themes/dotline-light.css'),
  resolve(root, 'src/themes/dotline-dark.css'),
];

/**
 * Extract the set of declared `--nf-*` custom properties from a CSS source.
 * A declaration is recognised as `--nf-<ident>` followed (after optional whitespace)
 * by `:`.
 */
function extractNfVars(source: string): Set<string> {
  const out = new Set<string>();
  const len = source.length;
  let i = 0;
  while (i < len) {
    // find next "--"
    if (source[i] === '-' && source[i + 1] === '-') {
      // identifier start
      let j = i + 2;
      while (j < len) {
        const c = source[j];
        if (c === undefined) break;
        const isIdent =
          (c >= 'a' && c <= 'z') ||
          (c >= 'A' && c <= 'Z') ||
          (c >= '0' && c <= '9') ||
          c === '-' ||
          c === '_';
        if (!isIdent) break;
        j += 1;
      }
      const name = source.slice(i, j);
      // skip whitespace
      let k = j;
      while (k < len && (source[k] === ' ' || source[k] === '\t')) k += 1;
      if (source[k] === ':' && name.startsWith('--nf-')) {
        out.add(name);
      }
      i = j;
      continue;
    }
    i += 1;
  }
  return out;
}

function main(): void {
  const semanticSource = readFileSync(semanticPath, 'utf8');
  const expected = extractNfVars(semanticSource);

  if (expected.size === 0) {
    console.error('check-theme-parity: no --nf-* tokens found in semantic.css');
    process.exit(1);
  }

  let failed = false;
  for (const path of themePaths) {
    const source = readFileSync(path, 'utf8');
    const have = extractNfVars(source);
    const missing: string[] = [];
    for (const name of expected) {
      if (!have.has(name)) missing.push(name);
    }
    const extra: string[] = [];
    for (const name of have) {
      if (!expected.has(name)) extra.push(name);
    }
    if (missing.length > 0 || extra.length > 0) {
      failed = true;
      console.error(`\n[theme-parity] ${path}`);
      if (missing.length > 0) {
        console.error(`  missing (${missing.length}):`);
        for (const m of missing.sort()) console.error(`    - ${m}`);
      }
      if (extra.length > 0) {
        console.error(`  extra (${extra.length}):`);
        for (const m of extra.sort()) console.error(`    + ${m}`);
      }
    }
  }

  if (failed) {
    console.error('\ncheck-theme-parity: FAILED');
    process.exit(1);
  }
  console.info(`check-theme-parity: OK (${expected.size} tokens x ${themePaths.length} themes)`);
}

main();
