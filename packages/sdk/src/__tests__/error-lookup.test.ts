/**
 * The lookup must cover the whole generated catalog, not a list somebody
 * remembered to extend. The interesting assertion is not "AI codes resolve"
 * — it is "every group the barrel exports resolves", which is what makes a
 * newly generated domain reachable without editing error-lookup.ts.
 */

import { describe, expect, it } from 'vitest';
import { ERROR_GROUPS, lookupErrorDefinition, lookupErrorI18nKey } from '../error-lookup';
import * as catalog from '../errors/index.js';

/** Barrel export names that look like a generated error group. */
const barrelGroupNames = Object.keys(catalog).filter((name) => name.endsWith('Errors'));

describe('ERROR_GROUPS', () => {
  it('collects every *Errors export the generated barrel provides', () => {
    expect(barrelGroupNames.length).toBeGreaterThan(0);
    expect(Object.keys(ERROR_GROUPS).sort()).toEqual([...barrelGroupNames].sort());
  });

  it('collects no export that is not an error group', () => {
    for (const name of Object.keys(ERROR_GROUPS)) {
      expect(name, `${name} is not a generated error group`).toMatch(/Errors$/);
    }
  });
});

describe('lookupErrorDefinition', () => {
  it('resolves every code in every generated domain', () => {
    const unresolved: string[] = [];
    for (const [groupName, group] of Object.entries(ERROR_GROUPS)) {
      for (const def of Object.values(group)) {
        if (lookupErrorDefinition(def.code) !== def) {
          unresolved.push(`${groupName}.${def.code}`);
        }
      }
    }
    expect(unresolved).toEqual([]);
  });

  it('covers the domains a hand-written group list has historically missed', () => {
    // A spot check with real codes from domains added after the original
    // hand-maintained list was written. If codegen ever drops these the
    // assertion above still fires, but this one names the casualty.
    for (const code of [
      'WS.COMMENT.NOT_FOUND',
      'AUTH.SESSION.UNAUTHORIZED',
      'VALIDATION.BODY.FIELD_INVALID',
    ]) {
      expect(lookupErrorDefinition(code), code).toBeDefined();
    }
  });

  it('returns undefined for unknown and empty codes', () => {
    expect(lookupErrorDefinition('NOT.A.REAL.CODE')).toBeUndefined();
    expect(lookupErrorDefinition('')).toBeUndefined();
    expect(lookupErrorDefinition(undefined)).toBeUndefined();
  });

  it('never maps two definitions onto the same code', () => {
    const seen = new Map<string, string>();
    const duplicates: string[] = [];
    for (const [groupName, group] of Object.entries(ERROR_GROUPS)) {
      for (const def of Object.values(group)) {
        const previous = seen.get(def.code);
        if (previous !== undefined) {
          duplicates.push(`${def.code} in ${previous} and ${groupName}`);
        }
        seen.set(def.code, groupName);
      }
    }
    expect(duplicates).toEqual([]);
  });
});

describe('lookupErrorI18nKey', () => {
  it('returns the catalog i18nKey when the entry carries one', () => {
    const withKey = Object.values(ERROR_GROUPS)
      .flatMap((group) => Object.values(group))
      .find((def) => def.i18nKey !== undefined);
    expect(withKey, 'no generated error entry declares an i18nKey').toBeDefined();
    if (withKey) {
      expect(lookupErrorI18nKey(withKey.code)).toBe(withKey.i18nKey);
    }
  });

  it('returns undefined for a known code without an i18nKey', () => {
    const withoutKey = Object.values(ERROR_GROUPS)
      .flatMap((group) => Object.values(group))
      .find((def) => def.i18nKey === undefined);
    if (withoutKey) {
      expect(lookupErrorI18nKey(withoutKey.code)).toBeUndefined();
    }
  });
});
