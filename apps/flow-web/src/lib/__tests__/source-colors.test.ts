/**
 * Unit tests for the centralised source-color exports. These guard
 * against accidental drift back into per-feature hex duplicates and
 * confirm every literal still matches the 6-digit `#rrggbb` shape that
 * the API and the state-graph SVG depend on.
 */

import { describe, expect, it } from 'vitest';

import { COLOR_PALETTE } from '../../features/calendars/general-tab';
import {
  CALENDAR_EVENT_PALETTE,
  CONSTRAINT_STATE_COLORS,
  INTEGRATION_SOURCE_COLORS,
} from '../source-colors';

const HEX_PATTERN = /^#[0-9a-fA-F]{6}$/;

describe('CALENDAR_EVENT_PALETTE', () => {
  it('is non-empty', () => {
    expect(CALENDAR_EVENT_PALETTE.length).toBeGreaterThan(0);
  });

  it('exposes well-formed 6-digit hex values', () => {
    for (const swatch of CALENDAR_EVENT_PALETTE) {
      expect(swatch.hex).toMatch(HEX_PATTERN);
    }
  });

  it('pairs every swatch with a design-token reference and i18n key', () => {
    for (const swatch of CALENDAR_EVENT_PALETTE) {
      expect(swatch.token).toMatch(/^var\(--nf-cal-color-\d+\)$/);
      expect(swatch.nameKey.length).toBeGreaterThan(0);
    }
  });

  it('contains no duplicate hex values', () => {
    const seen = new Set<string>();
    for (const swatch of CALENDAR_EVENT_PALETTE) {
      const lower = swatch.hex.toLowerCase();
      expect(seen.has(lower)).toBe(false);
      seen.add(lower);
    }
  });
});

describe('CONSTRAINT_STATE_COLORS', () => {
  it('is non-empty', () => {
    expect(Object.keys(CONSTRAINT_STATE_COLORS).length).toBeGreaterThan(0);
  });

  it('exposes well-formed 6-digit hex values', () => {
    for (const value of Object.values(CONSTRAINT_STATE_COLORS)) {
      expect(value).toMatch(HEX_PATTERN);
    }
  });

  it('covers the three external integration sources used by the state graph', () => {
    expect(CONSTRAINT_STATE_COLORS).toMatchObject({
      github: expect.stringMatching(HEX_PATTERN),
      slack: expect.stringMatching(HEX_PATTERN),
      google: expect.stringMatching(HEX_PATTERN),
    });
  });
});

describe('INTEGRATION_SOURCE_COLORS', () => {
  it('is non-empty', () => {
    expect(Object.keys(INTEGRATION_SOURCE_COLORS).length).toBeGreaterThan(0);
  });

  it('exposes well-formed 6-digit hex values', () => {
    for (const value of Object.values(INTEGRATION_SOURCE_COLORS)) {
      expect(value).toMatch(HEX_PATTERN);
    }
  });

  it('covers every category used by event-card.tsx', () => {
    const required = ['github', 'slack', 'google', 'signal', 'ai', 'task'] as const;
    for (const key of required) {
      expect(INTEGRATION_SOURCE_COLORS[key]).toMatch(HEX_PATTERN);
    }
  });

  it('agrees with CONSTRAINT_STATE_COLORS on overlapping brand keys', () => {
    expect(INTEGRATION_SOURCE_COLORS.github).toBe(CONSTRAINT_STATE_COLORS.github);
    expect(INTEGRATION_SOURCE_COLORS.slack).toBe(CONSTRAINT_STATE_COLORS.slack);
    expect(INTEGRATION_SOURCE_COLORS.google).toBe(CONSTRAINT_STATE_COLORS.google);
  });
});

describe('feature re-exports', () => {
  it('general-tab.COLOR_PALETTE is the shared CALENDAR_EVENT_PALETTE instance', () => {
    expect(COLOR_PALETTE).toBe(CALENDAR_EVENT_PALETTE);
  });

  it('snapshots the canonical palette to detect drift', () => {
    expect(CALENDAR_EVENT_PALETTE.map((s) => s.hex)).toMatchInlineSnapshot(`
      [
        "#2563eb",
        "#0891b2",
        "#16a34a",
        "#ca8a04",
        "#ea580c",
        "#dc2626",
        "#db2777",
        "#9333ea",
        "#475569",
        "#0f172a",
      ]
    `);
    expect(CONSTRAINT_STATE_COLORS).toMatchInlineSnapshot(`
      {
        "github": "#6e5494",
        "google": "#4285f4",
        "slack": "#4a154b",
      }
    `);
    expect(INTEGRATION_SOURCE_COLORS).toMatchInlineSnapshot(`
      {
        "ai": "#10b981",
        "github": "#6e5494",
        "google": "#4285f4",
        "signal": "#0ea5e9",
        "slack": "#4a154b",
        "task": "#f59e0b",
      }
    `);
  });
});
