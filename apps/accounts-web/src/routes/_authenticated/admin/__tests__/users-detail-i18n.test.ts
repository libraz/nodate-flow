/**
 * Lock the localized labels rendered by /admin/users/$userId. The
 * sessions table previously hardcoded a literal "IP" column header —
 * this test guards against that regression by asserting the
 * `users.ip_address` key exists in every supported locale and that
 * its value is non-empty.
 *
 * Mirrors the existing settings-i18n.test.ts style: locale-only
 * assertions keep the test fast and decoupled from SDK / router
 * mocks.
 */

import { describe, expect, it } from 'vitest';

import enAdmin from '../../../../../locales/en/admin.json';
import jaAdmin from '../../../../../locales/ja/admin.json';
import zhAdmin from '../../../../../locales/zh/admin.json';

describe('admin user-detail i18n', () => {
  it('defines users.ip_address in every supported locale', () => {
    for (const [lng, bundle] of [
      ['en', enAdmin],
      ['ja', jaAdmin],
      ['zh', zhAdmin],
    ] as const) {
      const value = bundle.users.ip_address;
      expect(value, `${lng}/admin.json users.ip_address`).toBeTypeOf('string');
      expect(value.length).toBeGreaterThan(0);
    }
  });

  it('defines users.user_agent and users.sessions alongside ip_address', () => {
    // Sanity-check the surrounding keys to catch accidental key reshuffles
    // that would silently break the sessions table column headers.
    for (const bundle of [enAdmin, jaAdmin, zhAdmin]) {
      expect(bundle.users.user_agent.length).toBeGreaterThan(0);
      expect(bundle.users.sessions.length).toBeGreaterThan(0);
    }
  });

  it('defines admin.title in every supported locale', () => {
    for (const [lng, bundle] of [
      ['en', enAdmin],
      ['ja', jaAdmin],
      ['zh', zhAdmin],
    ] as const) {
      const value = bundle.title;
      expect(value, `${lng}/admin.json title`).toBeTypeOf('string');
      expect(value.length).toBeGreaterThan(0);
    }
  });
});
