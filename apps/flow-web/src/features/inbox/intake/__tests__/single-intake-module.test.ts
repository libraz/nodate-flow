/**
 * There is one intake data module, not two.
 *
 * The app carried a second copy for a while: same endpoints, different
 * query keys, different page size, and nothing importing it. Nothing
 * broke visibly, which is why it lasted — but it made "what key does the
 * intake list cache under" a question with two answers, and the realtime
 * invalidation had picked the wrong one.
 */

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// vitest resolves the app root as its cwd.
const SRC = resolve(process.cwd(), 'src');

/** Every .ts/.tsx file under src, excluding tests. */
function sourceFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) {
      if (entry === '__tests__' || entry === 'node_modules') continue;
      sourceFiles(path, out);
      continue;
    }
    if (/\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry)) out.push(path);
  }
  return out;
}

describe('intake data module', () => {
  it('is declared in exactly one place', () => {
    const declaring = sourceFiles(SRC).filter((file) =>
      readFileSync(file, 'utf8').includes('export const intakeKeys'),
    );
    expect(declaring.map((f) => f.slice(SRC.length + 1))).toEqual(['features/inbox/intake/api.ts']);
  });
});
