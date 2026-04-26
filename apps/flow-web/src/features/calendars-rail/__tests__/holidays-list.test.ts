/**
 * holidays-list error fallback wiring.
 *
 * The subscribe handler previously rendered `err instanceof ApiError ?
 * err.message : t(fallback)`, which leaked raw upstream `detail`
 * strings into toasts. The handler now routes through
 * `formatApiError(err, t, 'calendars_rail.holidays.error')` so an
 * `ApiError.code` translates via the `errors` namespace and unknown
 * shapes degrade to the i18n fallback key.
 *
 * The test asserts the source wiring + locale contract directly so
 * future regressions to the legacy pattern fail loudly without
 * stubbing the SDK / toast surface.
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

const FALLBACK_KEY = 'calendars_rail.holidays.error';

describe('holidays-list error fallback', () => {
  const source = readFileSync(resolve(__dirname, '../holidays-list.tsx'), 'utf-8');

  describe('source wiring', () => {
    it('imports formatApiError from the lib helper', () => {
      expect(source).toMatch(/formatApiError/);
      expect(source).toMatch(/from\s+['"][.\\/]+lib\/api-error['"]/);
    });

    it('routes the subscribe onError through formatApiError with the holidays fallback key', () => {
      expect(source).toContain(`formatApiError(err, t, '${FALLBACK_KEY}')`);
    });

    it('drops the legacy err.message ternary', () => {
      expect(source).not.toMatch(/err\s+instanceof\s+ApiError\s*\?\s*err\.message/);
    });
  });

  describe('locale contract', () => {
    it('the fallback key resolves in en/ja/zh', () => {
      for (const locale of LOCALES) {
        const json = readCommon(locale);
        const value = getKeyPath(json, FALLBACK_KEY);
        expect(typeof value, `${locale} missing ${FALLBACK_KEY}`).toBe('string');
        expect(((value as string | undefined) ?? '').length).toBeGreaterThan(0);
      }
    });
  });
});
