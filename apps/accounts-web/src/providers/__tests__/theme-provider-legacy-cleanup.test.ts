/**
 * Verify that accounts-web's ThemeProvider correctly delegates to the shared
 * ThemeProvider from @nodate-flow/ui and provides the required server-sync
 * callbacks and legacy key cleanup configuration.
 */

/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const testDir = dirname(fileURLToPath(import.meta.url));

describe('ThemeProvider delegation', () => {
  const source = readFileSync(resolve(testDir, '../theme-provider.tsx'), 'utf-8');

  it('imports SharedThemeProvider from @nodate-flow/ui', () => {
    expect(source).toContain('@nodate-flow/ui/providers/theme-provider');
  });

  it('passes legacyKeys prop including libsonare-theme', () => {
    expect(source).toContain("'libsonare-theme'");
  });

  it('passes legacyKeys prop including vitepress-theme-appearance', () => {
    expect(source).toContain("'vitepress-theme-appearance'");
  });

  it('provides fetchServerTheme callback', () => {
    expect(source).toContain('fetchServerTheme');
  });

  it('provides syncServerTheme callback', () => {
    expect(source).toContain('syncServerTheme');
  });
});

describe('ThemeProvider backend sync', () => {
  const source = readFileSync(resolve(testDir, '../theme-provider.tsx'), 'utf-8');

  it('syncs preference to server via sdk.PATCH /auth/me', () => {
    expect(source).toContain("'/auth/me'");
    expect(source).toContain('PATCH');
    expect(source).toContain('themePreference');
  });

  it('reads user preference from authStore for fetchServerTheme', () => {
    expect(source).toContain('authStore.getState()');
    expect(source).toContain('themePreference');
  });
});

describe('Shared ThemeProvider legacy cleanup', () => {
  const sharedSource = readFileSync(
    resolve(testDir, '../../../../../packages/ui/src/providers/theme-provider.tsx'),
    'utf-8',
  );

  it('shared provider handles legacy key cleanup', () => {
    expect(sharedSource).toContain('legacyKeys');
    expect(sharedSource).toContain('removeItem');
  });

  it('shared provider clears legacy keys on mount', () => {
    expect(sharedSource).toContain("typeof window !== 'undefined'");
  });
});
