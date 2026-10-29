/**
 * The shared keyframes (`nf-fade-in`, `nf-slide-in-from-*`, ...) are global
 * names that CSS modules and layout.css reference by string. Losing the
 * stylesheet that defines them produces no build error: every `animation`
 * declaration still parses, it just names a keyframe that does not exist,
 * and every enter / reveal / step transition in the app silently stops.
 *
 * The test resolves the whole chain (entry CSS → package export → rule)
 * rather than matching the import line, so an import that points at a file
 * without the keyframes fails too.
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

describe('accounts-web entry stylesheet', () => {
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
