/**
 * The shared motion keyframes are global names that CSS modules reference
 * through `global(nf-…)`. Losing the entry import produces no build error
 * and no runtime warning — the `animation` declarations still parse, they
 * just name keyframes that do not exist, so every enter and exit animation
 * silently stops and `usePresence` exits fall back to their timeout.
 *
 * The test resolves the whole chain (entry CSS → motion stylesheet →
 * keyframes) rather than matching the import line, so an import that
 * points at a file without the keyframes fails too.
 */

/// <reference types="node" />
import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const testDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(testDir, '../../../../..');
const entryCss = resolve(testDir, '../main.css');

const IMPORT_SPECIFIER = '@nodate-flow/ui/styles/motion.css';

describe('flow-web entry stylesheet', () => {
  const source = readFileSync(entryCss, 'utf-8');

  it('imports the shared motion keyframes', () => {
    expect(source).toContain(IMPORT_SPECIFIER);
  });

  it('resolves that import to a stylesheet that defines the keyframes', () => {
    const exportsMap = JSON.parse(
      readFileSync(resolve(repoRoot, 'packages/ui/package.json'), 'utf-8'),
    ) as { exports: Record<string, string> };
    const target = exportsMap.exports['./styles/motion.css'];
    expect(target, 'packages/ui no longer exports ./styles/motion.css').toBeDefined();

    const resolved = resolve(repoRoot, 'packages/ui', target ?? '');
    expect(existsSync(resolved), `${resolved} does not exist`).toBe(true);

    const rules = readFileSync(resolved, 'utf-8');
    expect(rules).toContain('@keyframes nf-fade-in');
  });
});
