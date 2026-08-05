/**
 * The primitives barrel has to keep up with the directory.
 *
 * Six components — DatePicker, ErrorBoundary, Markdown, PageSkeleton,
 * ThemePicker, TimePicker — existed, were styled, were tested, and were
 * absent from `primitives/index.ts`. Nothing said so: the subpath import
 * kept working, so the components were usable and only the aggregate
 * import was short. Adding the six by hand fixes today and not tomorrow,
 * which is why this walks the directory instead.
 *
 * It also caught what the gap was hiding. `error-boundary` and
 * `error-fallback` both exported a public `ErrorFallbackProps`, two
 * unrelated types under one name; the collision could not surface while
 * only one of them reached the barrel.
 */

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

const PRIMITIVES = resolve(import.meta.dirname, '../src/primitives');
const BARREL = join(PRIMITIVES, 'index.ts');

/** Directories that are not a component: tests, and internals by convention. */
function componentDirs(): string[] {
  return readdirSync(PRIMITIVES)
    .filter((name) => !name.startsWith('_') && !name.startsWith('.'))
    .filter((name) => statSync(join(PRIMITIVES, name)).isDirectory())
    .filter((name) => {
      try {
        // The entry module is named after its directory.
        return statSync(join(PRIMITIVES, name, `${name}.tsx`)).isFile();
      } catch {
        return false;
      }
    })
    .sort();
}

const barrel = readFileSync(BARREL, 'utf8');

describe('primitives barrel', () => {
  it('re-exports every component directory', () => {
    const missing = componentDirs().filter((d) => !barrel.includes(`from './${d}/${d}'`));
    expect(missing).toEqual([]);
  });

  it('re-exports every public name each component declares', () => {
    const gaps: string[] = [];
    for (const dir of componentDirs()) {
      const src = readFileSync(join(PRIMITIVES, dir, `${dir}.tsx`), 'utf8');
      for (const m of src.matchAll(
        /^export\s+(?:declare\s+)?(?:interface|type|const|function|class)\s+([A-Za-z_]\w*)/gm,
      )) {
        const name = m[1];
        if (name === undefined) continue;
        if (!new RegExp(`\\b${name}\\b`).test(barrel)) gaps.push(`${dir}: ${name}`);
      }
    }
    expect(gaps).toEqual([]);
  });

  it('exports no name twice', () => {
    const seen = new Map<string, number>();
    for (const m of barrel.matchAll(/(?:^|[{,]\s*)(?:default as\s+)?([A-Z]\w*)(?=\s*[,}])/gm)) {
      const name = m[1];
      if (name === undefined) continue;
      seen.set(name, (seen.get(name) ?? 0) + 1);
    }
    const duplicates = [...seen].filter(([, n]) => n > 1).map(([name]) => name);
    expect(duplicates).toEqual([]);
  });
});
