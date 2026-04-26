/**
 * calendar-memos-panel error fallback wiring.
 *
 * The panel previously rendered `err instanceof ApiError ? err.message :
 * t(fallback)`, which leaked raw upstream `detail` strings into toasts
 * for the add / update / delete handlers. The panel now routes every
 * `onError` through `formatApiError(err, t, fallbackKey)` so an
 * `ApiError.code` resolves to a translated string and unknown shapes
 * degrade to the i18n fallback key.
 *
 * The test exercises three guarantees:
 *   1. `formatApiError` is imported from the lib helper.
 *   2. The legacy `instanceof ApiError ? err.message :` ternary is gone.
 *   3. Each fallback key exists in en/ja/zh and is non-empty.
 *
 * Mounting the panel would require a fully stubbed SDK + drawer +
 * confirm-action surface; the source-level guards cover the branch
 * decisions directly without paying that cost. A canonical integration
 * guard already lives in `lib/__tests__/api-error-call-sites.test.ts`.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const LOCALES = ['en', 'ja', 'zh'] as const;
type Locale = (typeof LOCALES)[number];

function getKeyPath(obj: unknown, path: string): unknown {
  let cur: unknown = obj;
  for (const segment of path.split('.')) {
    if (cur === null || cur === undefined || typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[segment];
  }
  return cur;
}

function readCommon(locale: Locale): unknown {
  return JSON.parse(
    readFileSync(resolve(__dirname, `../../../../locales/${locale}/common.json`), 'utf-8'),
  );
}

const FALLBACK_KEYS = [
  'calendar.memos.add_error',
  'calendar.memos.update_error',
  'calendar.memos.delete_error',
] as const;

describe('calendar-memos-panel error fallback', () => {
  const source = readFileSync(resolve(__dirname, '../calendar-memos-panel.tsx'), 'utf-8');

  describe('source wiring', () => {
    it('imports formatApiError from the lib helper', () => {
      expect(source).toMatch(/formatApiError/);
      expect(source).toMatch(/from\s+['"][.\\/]+lib\/api-error['"]/);
    });

    it('wires every onError through formatApiError with its fallback key', () => {
      for (const key of FALLBACK_KEYS) {
        expect(source).toContain(`'${key}'`);
        expect(source).toContain(`formatApiError(err, t, '${key}')`);
      }
    });

    it('drops the legacy err.message ternary', () => {
      expect(source).not.toMatch(/err\s+instanceof\s+ApiError\s*\?\s*err\.message/);
    });
  });

  describe('locale contract', () => {
    it('every fallback key resolves in en/ja/zh', () => {
      for (const locale of LOCALES) {
        const json = readCommon(locale);
        for (const key of FALLBACK_KEYS) {
          const value = getKeyPath(json, key);
          expect(typeof value, `${locale} missing ${key}`).toBe('string');
          expect(((value as string | undefined) ?? '').length).toBeGreaterThan(0);
        }
      }
    });
  });
});
