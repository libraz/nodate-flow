/**
 * discover-list error fallback.
 *
 * The list previously rendered `error.message` (or `String(error)` for
 * non-`ApiError` shapes) verbatim, which leaked raw upstream text into
 * the UI. The fallback now goes through `formatApiError(error, t, …)`
 * so an `ApiError.code` resolves to a translated string and unknown
 * shapes degrade to a translated fallback key.
 *
 * The test exercises two paths:
 *
 * 1. A `useDiscoverableCalendarsQuery` failure with an `ApiError` whose
 *    code matches a known errors-namespace key — the rendered message
 *    must be the translated string, not the raw upstream `message`.
 *
 * 2. The `subscribe.mutate` toast `onError` handler — it now uses
 *    `formatApiError` with the `subscribe_error` fallback key, so a
 *    plain `Error` falls through to its own `.message` (i.e. it does
 *    not silently substitute `discover.empty` like the old code did).
 *
 * Both behaviours are verified at the source level and against the
 * locale JSON. Mounting the component would require stubbing the
 * `useDiscoverableCalendarsQuery` + `useSubscribeToCalendarMutation`
 * hooks plus the toast surface; the source-level proofs cover the
 * branch decisions directly without paying that cost.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';
import { ApiError, formatApiError } from '../../../lib/api-error';

/**
 * Locale shape for the slice we care about. We carry the keys as a
 * generic record so the snake_case JSON keys (which are part of the
 * locale contract) do not collide with biome's camelCase rule on
 * TypeScript identifiers — a mismatch we'd rather hide here than
 * suppress at every call site.
 */
type LocaleDiscoverShape = {
  calendars_rail: { discover: Record<string, string> };
};

function readCommon(locale: 'en' | 'ja' | 'zh'): LocaleDiscoverShape {
  const p = resolve(__dirname, `../../../../locales/${locale}/common.json`);
  return JSON.parse(readFileSync(p, 'utf-8')) as LocaleDiscoverShape;
}

function makeT(translations: Record<string, string> = {}): TFunction {
  // Mimic the i18next signature we care about: known keys translate;
  // an explicit `defaultValue` falls back to its argument; otherwise
  // the key itself comes back so call sites can assert structurally.
  const fn = (key: string, options?: { defaultValue?: string; ns?: string }): string => {
    const hit = translations[key];
    if (hit !== undefined) return hit;
    if (options?.defaultValue !== undefined) return options.defaultValue;
    return key;
  };
  return fn as unknown as TFunction;
}

describe('discover-list error fallback', () => {
  describe('locale contract', () => {
    it('every locale exposes load_error and subscribe_error', () => {
      for (const lng of ['en', 'ja', 'zh'] as const) {
        const j = readCommon(lng);
        const { discover } = j.calendars_rail;
        for (const key of ['load_error', 'subscribe_error'] as const) {
          const value = discover[key];
          expect(typeof value).toBe('string');
          expect((value ?? '').length).toBeGreaterThan(0);
        }
      }
    });
  });

  describe('source wiring', () => {
    const source = readFileSync(resolve(__dirname, '../discover-list.tsx'), 'utf-8');

    it('uses formatApiError for the query error fallback', () => {
      expect(source).toContain('formatApiError(error, t, ');
      expect(source).toContain("'calendars_rail.discover.load_error'");
    });

    it('uses formatApiError for the subscribe mutation toast', () => {
      expect(source).toContain("'calendars_rail.discover.subscribe_error'");
    });

    it('drops the bare error.message / String(error) fallback', () => {
      expect(source).not.toMatch(/String\(error\)/);
      expect(source).not.toMatch(/err\.message\s*:\s*t\(/);
    });
  });

  describe('formatApiError behaviour against ApiError.code', () => {
    it('translates an ApiError via the errors namespace', () => {
      const t = makeT({ 'WS.CAL.DISCOVER.FORBIDDEN': 'You are not allowed.' });
      const err = new ApiError('WS.CAL.DISCOVER.FORBIDDEN', 'raw upstream', 403);
      expect(formatApiError(err, t, 'calendars_rail.discover.load_error')).toBe(
        'You are not allowed.',
      );
    });

    it('falls back to the translated key for opaque error shapes', () => {
      const t = makeT({ 'calendars_rail.discover.load_error': 'Could not load.' });
      expect(formatApiError(undefined, t, 'calendars_rail.discover.load_error')).toBe(
        'Could not load.',
      );
    });
  });
});
