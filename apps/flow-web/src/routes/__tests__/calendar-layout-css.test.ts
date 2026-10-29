/**
 * Wherever /calendar's month grid is given a definite row span, it must
 * also get an explicit column. Grid auto-placement places definite-row
 * items first, so a row span alone puts the grid in the rail's column.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const css = readFileSync(resolve(__dirname, '../_authenticated.calendar.module.css'), 'utf8');

describe('/calendar two-column layout', () => {
  it('pins the grid column to a track wherever it spans rows', () => {
    const rules = [...css.matchAll(/\.gridColumn \{([^}]*)\}/g)].map((m) => m[1] ?? '');
    const spanning = rules.filter((body) => /grid-row:\s*1 \/ span 2\s*;/.test(body));
    expect(spanning.length).toBeGreaterThan(0);
    for (const body of spanning) {
      expect(body).toMatch(/grid-column:\s*2\s*;/);
    }
  });
});
