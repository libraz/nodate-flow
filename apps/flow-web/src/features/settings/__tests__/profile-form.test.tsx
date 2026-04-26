/**
 * profile-form locale picker labels.
 *
 * Asserts the language SegmentedControl renders the language native names
 * ("English / 日本語 / 中文") regardless of the active UI locale, AND that
 * those labels round-trip through i18n keys (`profile.locale.en` etc.) so
 * a future translator can not accidentally drift them.
 *
 * The test has two parts:
 *
 * 1. **Locale JSON contract.** Load every locale's `settings.json` and
 *    confirm `profile.locale` is the expected `{label, en, ja, zh}` shape
 *    with the language's *own* native name in `en/ja/zh` across en/ja/zh
 *    locales (the native-name convention).
 *
 * 2. **Source-level wiring.** Confirm `profile-form.tsx` reads each
 *    label through `t('profile.locale.en' | 'ja' | 'zh')` rather than
 *    inlining the literal string. This is the contract the JSON test
 *    backs — together they prove the picker labels can not silently
 *    fall out of sync with the locale files.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

interface LocaleProfileShape {
  profile: {
    locale: {
      label: string;
      en: string;
      ja: string;
      zh: string;
    };
  };
}

function readSettings(locale: 'en' | 'ja' | 'zh'): LocaleProfileShape {
  const p = resolve(__dirname, `../../../../locales/${locale}/settings.json`);
  return JSON.parse(readFileSync(p, 'utf-8')) as LocaleProfileShape;
}

describe('profile-form locale picker', () => {
  describe('locale JSON contract', () => {
    it('en/ja/zh all share the {label, en, ja, zh} shape', () => {
      for (const lng of ['en', 'ja', 'zh'] as const) {
        const j = readSettings(lng);
        expect(typeof j.profile.locale.label).toBe('string');
        expect(typeof j.profile.locale.en).toBe('string');
        expect(typeof j.profile.locale.ja).toBe('string');
        expect(typeof j.profile.locale.zh).toBe('string');
      }
    });

    // The "native name" convention — the value of `profile.locale.<lng>`
    // is always the language's *own* name regardless of the surrounding
    // UI locale. Hardcoded here because the convention itself is the
    // assertion under test.
    it('renders English/日本語/中文 as labels in every locale', () => {
      for (const lng of ['en', 'ja', 'zh'] as const) {
        const j = readSettings(lng);
        expect(j.profile.locale.en).toBe('English');
        expect(j.profile.locale.ja).toBe('日本語');
        expect(j.profile.locale.zh).toBe('中文');
      }
    });
  });

  describe('source wiring', () => {
    const source = readFileSync(resolve(__dirname, '../profile-form.tsx'), 'utf-8');

    it('builds locale options from t(profile.locale.<lng>) keys', () => {
      expect(source).toContain("t('profile.locale.en')");
      expect(source).toContain("t('profile.locale.ja')");
      expect(source).toContain("t('profile.locale.zh')");
    });

    it('does not inline the language native names as literals', () => {
      // Match the SegmentedControlOption literal that previously
      // hardcoded `label: 'English'` / `label: '日本語'` / `label: '中文'`.
      // The new code routes through t(), so the inline form must be gone.
      expect(source).not.toMatch(/label:\s*'English'/);
      expect(source).not.toMatch(/label:\s*'日本語'/);
      expect(source).not.toMatch(/label:\s*'中文'/);
    });

    it('uses profile.locale.label for the field accessible name', () => {
      // The FormField label / aria-label must use the dedicated `.label`
      // key now that `profile.locale` is an object, not a plain string.
      expect(source).toContain("t('profile.locale.label')");
      expect(source).not.toMatch(/t\('profile\.locale'\)/);
    });
  });
});
