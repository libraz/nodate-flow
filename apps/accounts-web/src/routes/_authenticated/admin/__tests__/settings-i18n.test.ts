/**
 * /admin/settings — placeholder i18n contract.
 *
 * The numeric `<input>`s used to render `placeholder="0 = unlimited"`
 * inline. That literal would surface as English text on every locale.
 * The placeholder now reads through `t('settings.unlimited_placeholder')`,
 * and the asserts below guarantee:
 *
 * 1. Every locale defines the translation key (no implicit English leak).
 * 2. `settings.tsx` no longer carries the literal placeholder string.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const LOCALES = ['en', 'ja', 'zh'] as const;
type Locale = (typeof LOCALES)[number];

interface AdminLocale {
  settings: Record<string, string>;
}

function readAdmin(locale: Locale): AdminLocale {
  const p = resolve(__dirname, '..', '..', '..', '..', '..', 'locales', locale, 'admin.json');
  return JSON.parse(readFileSync(p, 'utf-8')) as AdminLocale;
}

describe('admin/settings unlimited placeholder', () => {
  it('every locale defines settings.unlimited_placeholder', () => {
    for (const locale of LOCALES) {
      const json = readAdmin(locale);
      const value = json.settings?.unlimited_placeholder;
      expect(typeof value, `${locale}/admin.json missing unlimited_placeholder`).toBe('string');
      expect((value ?? '').length).toBeGreaterThan(0);
    }
  });

  it('settings.tsx routes the placeholder through t()', () => {
    const source = readFileSync(resolve(__dirname, '..', 'settings.tsx'), 'utf-8');
    expect(source).toContain("t('settings.unlimited_placeholder')");
    expect(source).not.toContain('placeholder="0 = unlimited"');
  });
});
