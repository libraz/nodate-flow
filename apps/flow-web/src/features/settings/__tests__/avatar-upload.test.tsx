/**
 * avatar-upload accessibility — icon-only button labelling.
 *
 * The circular preview in `<AvatarUpload>` is an icon-only file-picker
 * trigger (avatar/initials + Camera glyph overlay, no visible text).
 * Without a deliberate `aria-label` it would expose only the inner
 * initials or alt text, leaving screen readers without a verb.
 *
 * The test asserts two things:
 *
 * 1. **Locale JSON contract.** Every locale carries the
 *    `upload_aria` / `replace_aria` / `delete_aria` keys so
 *    translators see them as first-class strings.
 *
 * 2. **Source wiring.** `avatar-upload.tsx` reads the preview button's
 *    accessible name from `t('settings:profile.avatar.upload_aria'|
 *    'replace_aria')` based on whether the user already has an avatar.
 *    Hard-coded labels would drift from the locale files.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

/**
 * Locale shape for the slice we care about. Snake_case JSON keys are
 * carried as a generic `Record<string, string>` so the i18n contract
 * does not collide with biome's camelCase identifier rule.
 */
interface LocaleAvatarShape {
  profile: { avatar: Record<string, string> };
}

function readSettings(locale: 'en' | 'ja' | 'zh'): LocaleAvatarShape {
  const p = resolve(__dirname, `../../../../locales/${locale}/settings.json`);
  return JSON.parse(readFileSync(p, 'utf-8')) as LocaleAvatarShape;
}

describe('avatar-upload icon-only buttons', () => {
  describe('locale JSON contract', () => {
    it('every locale defines upload/replace/delete aria-label keys', () => {
      for (const lng of ['en', 'ja', 'zh'] as const) {
        const j = readSettings(lng);
        const { avatar } = j.profile;
        for (const key of ['upload_aria', 'replace_aria', 'delete_aria'] as const) {
          const value = avatar[key];
          expect(typeof value).toBe('string');
          expect((value ?? '').length).toBeGreaterThan(0);
        }
      }
    });
  });

  describe('source wiring', () => {
    const source = readFileSync(resolve(__dirname, '../avatar-upload.tsx'), 'utf-8');

    it('routes the icon-only preview button through t() for its aria-label', () => {
      // The preview <button> element must read its accessible name from
      // the dedicated aria keys, not from the visible-text labels.
      expect(source).toContain("t('settings:profile.avatar.upload_aria')");
      expect(source).toContain("t('settings:profile.avatar.replace_aria')");
    });

    it('attaches aria-label to the icon-only preview button', () => {
      // The preview is a <button className={styles.preview}> with the
      // computed aria-label spread on it. Both the className and the
      // aria-label assignment must be present.
      expect(source).toMatch(/className=\{styles\.preview\}/);
      expect(source).toMatch(/aria-label=\{previewAriaLabel\}/);
    });
  });
});
