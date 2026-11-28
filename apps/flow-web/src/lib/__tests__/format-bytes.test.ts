/**
 * Unit tests for the i18n-aware formatBytes helper, driven by the shipped
 * `common` locale files so a missing unit key fails the test.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import i18next, { type TFunction } from 'i18next';
import { beforeAll, describe, expect, it } from 'vitest';

import { formatBytes } from '../format-bytes';

const LOCALES_DIR = resolve(__dirname, '../../../locales');

function loadCommon(lng: string): Record<string, unknown> {
  return JSON.parse(readFileSync(resolve(LOCALES_DIR, lng, 'common.json'), 'utf8'));
}

let tEn: TFunction;
let tJa: TFunction;

beforeAll(async () => {
  const instance = i18next.createInstance();
  await instance.init({
    lng: 'en',
    fallbackLng: false,
    defaultNS: 'common',
    ns: ['common'],
    resources: { en: { common: loadCommon('en') }, ja: { common: loadCommon('ja') } },
    interpolation: { escapeValue: false },
  });
  tEn = instance.getFixedT('en', 'common');
  tJa = instance.getFixedT('ja', 'common');
});

describe('formatBytes', () => {
  it('returns bytes without fractional digits', () => {
    expect(formatBytes(0, 'en-US', tEn)).toBe('0 B');
    expect(formatBytes(512, 'en-US', tEn)).toBe('512 B');
  });

  it('promotes to KB at 1024', () => {
    expect(formatBytes(1024, 'en-US', tEn)).toBe('1 KB');
    expect(formatBytes(1536, 'en-US', tEn)).toBe('1.5 KB');
  });

  it('promotes through MB / GB / TB', () => {
    expect(formatBytes(1024 * 1024, 'en-US', tEn)).toBe('1 MB');
    expect(formatBytes(1024 * 1024 * 1024, 'en-US', tEn)).toBe('1 GB');
    expect(formatBytes(1024 ** 4, 'en-US', tEn)).toBe('1 TB');
  });

  it('uses the locale formatter for the value', () => {
    const formatted = formatBytes(1024 * 1024 * 2.5, 'ja-JP', tJa);
    expect(formatted).toContain('2.5');
    expect(formatted).not.toContain('file_size');
  });

  it('caps at the largest known unit (TB)', () => {
    expect(formatBytes(1024 ** 5, 'en-US', tEn)).toContain('TB');
  });
});
