/// <reference types="node" />
/**
 * Verify that time-web calendar components use i18n `t()` calls for
 * aria-label attributes instead of hardcoded English strings (H8).
 *
 * Scans the source of key calendar components to ensure every aria-label
 * is a `t('...')` call, not a plain string literal.
 */

import { readFileSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const calendarDir = resolve(import.meta.dirname, '..');

/** Read all .tsx files in the calendar feature directory. */
function getCalendarSources(): Array<{ name: string; content: string }> {
  return readdirSync(calendarDir)
    .filter((f) => f.endsWith('.tsx'))
    .map((f) => ({
      name: f,
      content: readFileSync(resolve(calendarDir, f), 'utf-8'),
    }));
}

describe('calendar aria-label i18n', () => {
  const sources = getCalendarSources();

  it('finds at least one calendar component with aria-label', () => {
    const withAriaLabel = sources.filter((s) => s.content.includes('aria-label'));
    expect(withAriaLabel.length).toBeGreaterThan(0);
  });

  it('every aria-label uses a t() call, not a hardcoded string', () => {
    // Pattern: aria-label="Some hardcoded string" (with a plain string, not JSX expression)
    // Valid: aria-label={t('...')} or aria-label={someVariable}
    // Invalid: aria-label="Close" or aria-label="Previous month"
    const hardcodedPattern = /aria-label="[^{][^"]*[a-zA-Z][^"]*"/g;

    for (const { name, content } of sources) {
      const matches = content.match(hardcodedPattern) ?? [];
      // Filter out false positives from comments or non-JSX contexts
      const actual = matches.filter((m) => {
        // Skip if it's inside a comment line
        const idx = content.indexOf(m);
        const lineStart = content.lastIndexOf('\n', idx);
        const line = content.slice(lineStart, idx).trim();
        return !line.startsWith('//') && !line.startsWith('*');
      });
      expect(actual, `${name} has hardcoded aria-label values: ${actual.join(', ')}`).toHaveLength(
        0,
      );
    }
  });

  it('calendar-header.tsx uses t() for navigation aria-labels', () => {
    const header = sources.find((s) => s.name === 'calendar-header.tsx');
    expect(header).toBeDefined();
    const content = header?.content ?? '';

    // Previous/next buttons must use translated labels
    expect(content).toContain("t('calendar.previous_month')");
    expect(content).toContain("t('calendar.next_month')");
    expect(content).toContain("t('calendar.previous_week')");
    expect(content).toContain("t('calendar.next_week')");
    expect(content).toContain("t('calendar.toggle_sidebar')");
    expect(content).toContain("t('search.search_events')");
  });

  it('all t() keys used in aria-labels exist in en/common.json', () => {
    const enJson = readFileSync(
      resolve(import.meta.dirname, '../../../locales/en/common.json'),
      'utf-8',
    );
    const translations = JSON.parse(enJson);

    // Collect all t('...') calls from aria-label attributes across all sources
    const tCallPattern = /aria-label=\{t\('([^']+)'\)/g;
    const usedKeys: string[] = [];
    for (const { content } of sources) {
      let match: RegExpExecArray | null = tCallPattern.exec(content);
      while (match !== null) {
        if (match[1]) usedKeys.push(match[1]);
        match = tCallPattern.exec(content);
      }
    }

    expect(usedKeys.length).toBeGreaterThan(0);

    for (const key of usedKeys) {
      // Navigate the nested JSON using dot-separated key parts
      const parts = key.split('.');
      let value: unknown = translations;
      for (const part of parts) {
        expect(value, `Missing translation key: ${key}`).toHaveProperty(part);
        value = (value as Record<string, unknown>)[part];
      }
      expect(typeof value, `Translation for ${key} should be a string`).toBe('string');
    }
  });
});
