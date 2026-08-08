/**
 * Region helpers.
 *
 * The country allowlist is a second copy of `supportedCountries` in
 * `packages/go-shared/region/region.go`, and the two are compared by
 * `scripts/check-region-parity.mjs` rather than from here: that comparison
 * has to read a file outside this package, and the SDK ships to browser
 * bundles with `"types": []` so shipped code cannot reach for Node globals.
 * See the note in `public-paths.test.ts`. What stays here is what can be
 * asserted from the module itself.
 */

import { describe, expect, it } from 'vitest';

import {
  detectTimezone,
  formatTimezoneLabel,
  groupTimezonesByRegion,
  listSupportedTimezones,
  SUPPORTED_COUNTRIES,
} from '../region';

describe('SUPPORTED_COUNTRIES', () => {
  it('is a non-trivial list', () => {
    expect(Object.keys(SUPPORTED_COUNTRIES).length).toBeGreaterThan(20);
  });

  it('uses uppercase ISO 3166-1 alpha-2 codes throughout', () => {
    for (const code of Object.keys(SUPPORTED_COUNTRIES)) {
      expect(code, code).toMatch(/^[A-Z]{2}$/);
    }
  });

  it('gives every code a non-empty display name', () => {
    for (const [code, name] of Object.entries(SUPPORTED_COUNTRIES)) {
      expect(name, code).not.toBe('');
    }
  });
});

describe('timezone helpers', () => {
  it('always offers UTC first so the backend default is selectable', () => {
    expect(listSupportedTimezones()[0]).toBe('UTC');
  });

  it('detects a timezone that is in the offered list', () => {
    expect(listSupportedTimezones()).toContain(detectTimezone());
  });

  it('puts slashless zones in a leading Global group', () => {
    const groups = groupTimezonesByRegion(['Asia/Tokyo', 'UTC', 'Europe/Paris']);
    expect(groups[0]).toEqual({ region: 'Global', zones: ['UTC'] });
    expect(groups.map((g) => g.region)).toEqual(['Global', 'Asia', 'Europe']);
  });

  it('loses no zone when grouping', () => {
    const all = listSupportedTimezones();
    const regrouped = groupTimezonesByRegion(all).flatMap((g) => g.zones);
    expect(regrouped.sort()).toEqual([...all].sort());
  });

  it('strips the region prefix and underscores from a label', () => {
    expect(formatTimezoneLabel('America/Argentina/Buenos_Aires')).toBe('Argentina/Buenos Aires');
    expect(formatTimezoneLabel('UTC')).toBe('UTC');
  });
});
