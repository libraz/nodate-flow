import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

describe('reduced motion token cascade', () => {
  it('uses higher specificity than theme selectors loaded later by apps', () => {
    const base = readFileSync(join(process.cwd(), 'src/tokens/base.css'), 'utf8');
    const semantic = readFileSync(join(process.cwd(), 'src/tokens/semantic.css'), 'utf8');

    expect(base).toContain('@media (prefers-reduced-motion: reduce)');
    expect(base).toContain(':root:root');
    expect(base).toContain('--nf-motion-duration: 0ms');
    expect(semantic).toContain('@media (prefers-reduced-motion: reduce)');
    expect(semantic).toContain(':root:root');
    expect(semantic).toContain('--nf-surface-blur: 0px');
  });
});
