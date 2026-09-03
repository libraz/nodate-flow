/**
 * Toast keys that no locale defined.
 *
 * Four call sites asked i18next for keys that existed in no catalog, so
 * a failed role change, a failed member removal, or a clipboard denial
 * put the raw key — `workspaces.members.remove_failed` — into a red
 * toast. i18next returns the key when a lookup misses, which is exactly
 * why the bug survived: nothing throws, and the English UI looks only
 * slightly odd.
 *
 * The generalized `make i18n-check` compares locales against each other
 * and cannot see this class of gap, because a key absent everywhere is
 * consistent. This test closes that side: it reads the call sites and
 * requires every key they name to resolve in every language.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const LOCALES = ['en', 'ja', 'zh'] as const;

/** Keys pulled from the call sites that emit them, with their namespace. */
const REQUIRED: ReadonlyArray<{ source: string; ns: string; keys: readonly string[] }> = [
  {
    source: '../../workspaces/workspace-members-table.tsx',
    ns: 'common',
    keys: ['workspaces.members.role_update_failed', 'workspaces.members.remove_failed'],
  },
  {
    source: '../totp-panel.tsx',
    ns: 'settings',
    keys: ['security.totp.recovery.copy_failed', 'security.totp.secret_copy_failed'],
  },
];

function lookup(catalog: unknown, key: string): unknown {
  let cur: unknown = catalog;
  for (const part of key.split('.')) {
    if (cur === null || typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[part];
  }
  return cur;
}

function loadCatalog(lang: string, ns: string): unknown {
  const path = resolve(__dirname, `../../../../locales/${lang}/${ns}.json`);
  return JSON.parse(readFileSync(path, 'utf8')) as unknown;
}

describe('toast keys referenced by settings and workspace members', () => {
  for (const { source, ns, keys } of REQUIRED) {
    for (const key of keys) {
      it(`${ns}:${key} resolves in every language`, () => {
        for (const lang of LOCALES) {
          const value = lookup(loadCatalog(lang, ns), key);
          expect(typeof value, `${lang}/${ns}.json is missing ${key}`).toBe('string');
          expect((value as string).length).toBeGreaterThan(0);
        }
      });

      it(`${ns}:${key} is still the key the code asks for`, () => {
        // Guards the pairing rather than the copy: renaming the key in
        // the component without moving the catalog entry would otherwise
        // reintroduce the raw-key toast silently.
        const code = readFileSync(resolve(__dirname, source), 'utf8');
        expect(code).toContain(key);
      });
    }
  }
});
