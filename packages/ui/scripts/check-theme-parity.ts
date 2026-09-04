/**
 * check-theme-parity
 *
 * Asserts that every CSS custom property declared in src/tokens/semantic.css
 * is also declared in each of the 6 theme files. Exits non-zero on any miss.
 *
 * Intentionally avoids regular expressions: we walk the file character by character
 * looking for `--nf-` identifiers preceded by start-of-line / whitespace and
 * followed by `:`.
 *
 * The self-verification cases run on every invocation, before any file is
 * read, and a failure among them stops the run. The extractor is the whole
 * check: if it stopped recognising a declaration it would read every theme
 * as empty, and the empty-semantic guard below is the only thing standing
 * between that and a green run over six themes nobody compared.
 */

import assert from 'node:assert/strict';
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
  resolve(root, 'src/themes/glass-light.css'),
  resolve(root, 'src/themes/glass-dark.css'),
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

/**
 * Which tokens a theme is missing, and which it declares that the semantic
 * layer never named.
 */
function parity(
  expected: ReadonlySet<string>,
  have: ReadonlySet<string>,
): { missing: string[]; extra: string[] } {
  const missing: string[] = [];
  for (const name of expected) {
    if (!have.has(name)) missing.push(name);
  }
  const extra: string[] = [];
  for (const name of have) {
    if (!expected.has(name)) extra.push(name);
  }
  return { missing, extra };
}

/* Self-verification. Runs before any file is read, every time. */

function selfCheck(): string[] {
  const cases: ReadonlyArray<[string, () => void]> = [
    [
      'reads a declaration',
      () => {
        assert.deepEqual([...extractNfVars('  --nf-color-fg: #111;')], ['--nf-color-fg']);
        assert.deepEqual([...extractNfVars('  --nf-space-2\t: 8px;')], ['--nf-space-2']);
      },
    ],
    [
      'does not read a use as a declaration',
      () => {
        assert.deepEqual([...extractNfVars('  color: var(--nf-color-fg);')], []);
        assert.deepEqual([...extractNfVars('  box-shadow: 0 0 0 var(--nf-space-1) red;')], []);
      },
    ],
    [
      'ignores a custom property outside the nf namespace',
      () => {
        assert.deepEqual([...extractNfVars('  --sc-inactive-fg: red;')], []);
      },
    ],
    [
      'reports a token missing from a theme and one only the theme declares',
      () => {
        const expected = new Set(['--nf-color-fg', '--nf-color-bg']);
        assert.deepEqual(parity(expected, new Set(['--nf-color-fg', '--nf-color-ink'])), {
          missing: ['--nf-color-bg'],
          extra: ['--nf-color-ink'],
        });
        assert.deepEqual(parity(expected, expected), { missing: [], extra: [] });
      },
    ],
  ];

  const failures: string[] = [];
  for (const [name, run] of cases) {
    try {
      run();
    } catch (err) {
      failures.push(`  ${name}\n    ${err instanceof Error ? err.message : String(err)}`);
    }
  }
  return failures;
}

function main(): void {
  const selfFailures = selfCheck();
  if (selfFailures.length > 0) {
    console.error(
      `check-theme-parity: ${selfFailures.length} self-verification case(s) failed, so no theme was read:\n`,
    );
    for (const f of selfFailures) console.error(f);
    console.error(
      '\nThe extractor itself is wrong. Fix it before trusting anything it says about the themes.',
    );
    process.exit(1);
  }

  const semanticSource = readFileSync(semanticPath, 'utf8');
  const expected = extractNfVars(semanticSource);

  if (expected.size === 0) {
    console.error('check-theme-parity: no --nf-* tokens found in semantic.css');
    process.exit(1);
  }

  let failed = false;
  for (const path of themePaths) {
    const source = readFileSync(path, 'utf8');
    const { missing, extra } = parity(expected, extractNfVars(source));
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
