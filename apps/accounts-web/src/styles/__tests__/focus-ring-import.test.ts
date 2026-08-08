/**
 * `nf-focus-ring` is a global utility class: JSX applies it, and the rule
 * that paints it lives in a stylesheet the app entry has to import. Losing
 * the import produces no build error and no runtime warning — the class is
 * still in the DOM, the ring simply never appears, and keyboard users lose
 * the focus indicator on every control that relies on it.
 *
 * The test resolves the whole chain (entry CSS → utility stylesheet → rule)
 * rather than matching the import line, so an import that points at a file
 * without the rule fails too.
 */

/// <reference types="node" />
import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const testDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(testDir, '../../../../..');
const entryCss = resolve(testDir, '../main.css');

const IMPORT_SPECIFIER = '@nodate-flow/ui/styles/focus-ring.css';

describe('accounts-web entry stylesheet', () => {
  const source = readFileSync(entryCss, 'utf-8');

  it('imports the focus-ring utility', () => {
    expect(source).toContain(IMPORT_SPECIFIER);
  });

  it('resolves that import to a stylesheet that defines the utility', () => {
    const exportsMap = JSON.parse(
      readFileSync(resolve(repoRoot, 'packages/ui/package.json'), 'utf-8'),
    ) as { exports: Record<string, string> };
    const target = exportsMap.exports['./styles/focus-ring.css'];
    expect(target, 'packages/ui no longer exports ./styles/focus-ring.css').toBeDefined();

    const resolved = resolve(repoRoot, 'packages/ui', target ?? '');
    expect(existsSync(resolved), `${resolved} does not exist`).toBe(true);

    const rules = readFileSync(resolved, 'utf-8');
    expect(rules).toContain('.nf-focus-ring');
    expect(rules).toContain('var(--nf-color-focus-ring)');
  });
});
