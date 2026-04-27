/**
 * GeneralTab — calendar color palette aria-label i18n contract.
 *
 * The 10-swatch picker used to expose the raw hex (`aria-label="#2563eb"`),
 * which screen readers verbalise as "hash two five six three e b" — useless
 * for the actor. Each swatch now carries an i18n `nameKey` and the picker
 * resolves it via `t()`.
 *
 * Two assertions:
 *
 * 1. Every swatch's `nameKey` resolves to a non-empty string in every
 *    locale (`en`, `ja`, `zh`).
 * 2. `general-tab.tsx` routes the swatch aria-label through `t()`.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

import { COLOR_PALETTE } from '../general-tab';

const LOCALES = ['en', 'ja', 'zh'] as const;
type Locale = (typeof LOCALES)[number];

function readCommon(locale: Locale): Record<string, unknown> {
  const p = resolve(__dirname, '..', '..', '..', '..', 'locales', locale, 'common.json');
  return JSON.parse(readFileSync(p, 'utf-8')) as Record<string, unknown>;
}

function lookup(json: Record<string, unknown>, dottedKey: string): unknown {
  const segments = dottedKey.split('.');
  let cursor: unknown = json;
  for (const segment of segments) {
    if (typeof cursor !== 'object' || cursor === null) return undefined;
    cursor = (cursor as Record<string, unknown>)[segment];
  }
  return cursor;
}

describe('calendar color-palette aria-label', () => {
  it('every swatch resolves a non-empty translated label in every locale', () => {
    for (const locale of LOCALES) {
      const json = readCommon(locale);
      for (const swatch of COLOR_PALETTE) {
        const value = lookup(json, swatch.nameKey);
        expect(typeof value, `${locale}/common.json missing ${swatch.nameKey}`).toBe('string');
        expect((value as string).length).toBeGreaterThan(0);
      }
    }
  });

  it('general-tab.tsx routes the swatch aria-label through t()', () => {
    const source = readFileSync(resolve(__dirname, '..', 'general-tab.tsx'), 'utf-8');
    expect(source).toContain('aria-label={t(swatch.nameKey)}');
    expect(source).not.toContain('aria-label={swatch.hex}');
  });
});
