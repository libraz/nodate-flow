/**
 * Lock the shared admin style objects:
 *   1. Every named export resolves to a non-empty CSSProperties record.
 *   2. Every CSS value either references a `var(--nf-*)` design token or
 *      is a non-token literal that is explicitly allowed (e.g.
 *      `'inline-block'` for `display`, numeric `fontWeight`, etc.).
 *
 * This guards against regressions where someone hard-codes a hex color or
 * raw rem value back into the shared module.
 */

import type { CSSProperties } from 'react';
import { describe, expect, it } from 'vitest';

import {
  adminBadgeBase,
  adminLabelStyle,
  adminTableStyle,
  adminTdStyle,
  adminThStyle,
  adminValueStyle,
} from '../styles';

const tokenRegex = /var\(--nf-/;

/**
 * Property values that are legitimately not token-backed. We allow a small
 * set of structural CSS keywords + numbers because tokens for them would be
 * over-engineering (e.g. there is no `--nf-display-inline-block`).
 */
const literalAllowList: ReadonlyArray<string | number> = [
  'inline-block',
  'start',
  'collapse',
  'nowrap',
  '100%',
  500,
  600,
];

function assertTokenized(name: string, style: CSSProperties): void {
  const entries = Object.entries(style);
  expect(entries.length, `${name} must define at least one property`).toBeGreaterThan(0);
  for (const [prop, value] of entries) {
    if (typeof value === 'string') {
      // multi-token shorthand is fine: `var(--nf-space-2) var(--nf-space-3)`
      // composite values may also embed tokens, e.g. `2px solid var(...)`.
      const referencesToken = tokenRegex.test(value);
      const isAllowedLiteral = literalAllowList.includes(value);
      expect(
        referencesToken || isAllowedLiteral,
        `${name}.${prop} = "${value}" must reference a --nf-* token (or be in the allow list)`,
      ).toBe(true);
    } else {
      // numeric values (fontWeight) — must be in the allow list
      expect(
        literalAllowList.includes(value as number),
        `${name}.${prop} numeric value ${String(value)} must be in the allow list`,
      ).toBe(true);
    }
  }
}

describe('admin styles', () => {
  it('exports every required style object', () => {
    expect(adminTableStyle).toBeTypeOf('object');
    expect(adminThStyle).toBeTypeOf('object');
    expect(adminTdStyle).toBeTypeOf('object');
    expect(adminLabelStyle).toBeTypeOf('object');
    expect(adminValueStyle).toBeTypeOf('object');
    expect(adminBadgeBase).toBeTypeOf('object');
  });

  it('routes every value through a design token (or an allow-listed literal)', () => {
    assertTokenized('adminTableStyle', adminTableStyle);
    assertTokenized('adminThStyle', adminThStyle);
    assertTokenized('adminTdStyle', adminTdStyle);
    assertTokenized('adminLabelStyle', adminLabelStyle);
    assertTokenized('adminValueStyle', adminValueStyle);
    assertTokenized('adminBadgeBase', adminBadgeBase);
  });

  it('does not reintroduce hard-coded rem / px literals', () => {
    const objects = [
      adminTableStyle,
      adminThStyle,
      adminTdStyle,
      adminLabelStyle,
      adminValueStyle,
      adminBadgeBase,
    ];
    for (const obj of objects) {
      for (const value of Object.values(obj)) {
        if (typeof value !== 'string') continue;
        // Reject any "0.125rem" / "8px" style hard-coded length, but allow
        // composites like "2px solid var(--nf-color-border)" since those
        // pair a hairline width with a token-backed color.
        const hasBareLength = /(?<!var\([^)]*)\b\d+(\.\d+)?(rem|px|em)\b/.test(value);
        if (hasBareLength) {
          // A composite is acceptable as long as it also contains a token.
          expect(
            tokenRegex.test(value),
            `value "${value}" mixes a length with no token reference`,
          ).toBe(true);
        }
      }
    }
  });
});
