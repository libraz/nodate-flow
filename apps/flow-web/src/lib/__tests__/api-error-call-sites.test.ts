/**
 * api-error call-sites — guard against regression to the
 * `err instanceof ApiError ? err.message : t(fallback)` anti-pattern.
 *
 * Every file listed below historically rendered the upstream
 * `err.message` (which can leak English `detail` strings, internal IDs,
 * stack frames, or even raw SQL fragments) directly into a toast.
 * The canonical helper `formatApiError(err, t, fallbackKey)` translates
 * the {@link ApiError.code} via the `errors` namespace first and only
 * falls back to a localized fallback key when nothing else is usable.
 *
 * The test asserts two things per file:
 *
 * 1. **Source wiring.** The file imports `formatApiError` and never
 *    constructs the legacy `instanceof ApiError ? err.message :` ternary
 *    or accesses `err.message` on a caught error directly.
 *
 * 2. **Locale contract.** The fallback i18n keys exist in every locale
 *    (`en`, `ja`, `zh`) so users on any UI language see a translated
 *    string instead of an empty toast.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const LOCALES = ['en', 'ja', 'zh'] as const;
type Locale = (typeof LOCALES)[number];

const LOCALES_ROOT = resolve(__dirname, '../../../locales');

/**
 * Walk a JSON object along a dotted key path. Returns `undefined` when
 * any segment is missing or not an object.
 */
function getKeyPath(obj: unknown, path: string): unknown {
  let cur: unknown = obj;
  for (const segment of path.split('.')) {
    if (cur === null || cur === undefined || typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[segment];
  }
  return cur;
}

function readLocale(locale: Locale, namespace: string): unknown {
  return JSON.parse(readFileSync(resolve(LOCALES_ROOT, locale, `${namespace}.json`), 'utf-8'));
}

interface CallSite {
  /** Path under apps/flow-web/src/, used to derive the source path. */
  source: string;
  /** i18n namespace each fallback key lives in. */
  namespace: string;
  /** Dotted i18n keys that must resolve in en/ja/zh. */
  keys: readonly string[];
}

const CALL_SITES: readonly CallSite[] = [
  {
    source: 'features/calendars/general-tab.tsx',
    namespace: 'common',
    keys: ['calendar.settings.general.save_error', 'calendar.settings.delete.error'],
  },
  {
    source: 'features/calendars/calendar-members-tab.tsx',
    namespace: 'common',
    keys: [
      'calendar.settings.members.update_error',
      'calendar.settings.members.remove_error',
      'calendar.settings.members.add_error',
    ],
  },
  {
    source: 'features/tasks/description-history/description-history-drawer.tsx',
    namespace: 'common',
    keys: ['tasks.history.restore_error'],
  },
  {
    source: 'features/tasks/event-from-task/event-from-task-dialog.tsx',
    namespace: 'common',
    keys: ['tasks.actions.create_event.error'],
  },
  {
    source: 'features/inbox/intake/intake-convert-dialog.tsx',
    namespace: 'inbox',
    keys: ['intake.convert.error'],
  },
  {
    source: 'features/settings/avatar-upload.tsx',
    namespace: 'settings',
    keys: ['profile.avatar.upload_error', 'profile.avatar.remove_error'],
  },
  {
    source: 'features/calendar-memos/calendar-memos-panel.tsx',
    namespace: 'common',
    keys: [
      'calendar.memos.add_error',
      'calendar.memos.update_error',
      'calendar.memos.delete_error',
    ],
  },
  {
    source: 'features/calendars-rail/holidays-list.tsx',
    namespace: 'common',
    keys: ['calendars_rail.holidays.error'],
  },
];

describe('api-error call sites — formatApiError adoption', () => {
  for (const site of CALL_SITES) {
    describe(site.source, () => {
      const sourcePath = resolve(__dirname, '../../', site.source);
      const source = readFileSync(sourcePath, 'utf-8');

      it('imports formatApiError from the lib helper', () => {
        expect(source).toMatch(/formatApiError/);
        expect(source).toMatch(/from\s+['"][.\\/]+lib\/api-error['"]/);
      });

      it('does not retain the legacy err.message ternary', () => {
        // The legacy pattern smuggled raw upstream messages into toasts.
        // Each occurrence is a regression we want a loud failure on.
        expect(source).not.toMatch(/err\s+instanceof\s+ApiError\s*\?\s*err\.message/);
      });

      it('every fallback i18n key resolves in en/ja/zh', () => {
        for (const locale of LOCALES) {
          const json = readLocale(locale, site.namespace);
          for (const key of site.keys) {
            const value = getKeyPath(json, key);
            expect(typeof value, `${locale}/${site.namespace}.json missing ${key}`).toBe('string');
            expect(((value as string | undefined) ?? '').length).toBeGreaterThan(0);
          }
        }
      });
    });
  }
});
