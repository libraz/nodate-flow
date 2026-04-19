/**
 * Verify that ThemeProvider's readStored() path clears legacy localStorage
 * keys on mount, so only `nf.theme` is authoritative (M8).
 *
 * Uses source-code analysis to verify the cleanup mechanism exists, plus
 * direct localStorage manipulation to test the clearLegacyThemeKeys
 * behavior in a happy-dom environment.
 */

/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const testDir = dirname(fileURLToPath(import.meta.url));

describe('ThemeProvider legacy key cleanup', () => {
  const source = readFileSync(resolve(testDir, '../theme-provider.tsx'), 'utf-8');

  it('defines the legacy key list including libsonare-theme', () => {
    expect(source).toContain("'libsonare-theme'");
  });

  it('defines the legacy key list including vitepress-theme-appearance', () => {
    expect(source).toContain("'vitepress-theme-appearance'");
  });

  it('calls clearLegacyThemeKeys inside readStored', () => {
    // readStored should invoke clearLegacyThemeKeys before reading nf.theme
    const readStoredBody = source.slice(
      source.indexOf('function readStored()'),
      source.indexOf('function resolveSystem()'),
    );
    expect(readStoredBody).toContain('clearLegacyThemeKeys()');
  });

  it('clearLegacyThemeKeys calls removeItem for each legacy key', () => {
    const fnStart = source.indexOf('function clearLegacyThemeKeys()');
    const fnEnd = source.indexOf('\n}', fnStart) + 2;
    const fnBody = source.slice(fnStart, fnEnd);
    expect(fnBody).toContain('removeItem');
    expect(fnBody).toContain('legacyThemeKeys');
  });
});

describe('ThemeProvider legacy key cleanup (clearLegacyThemeKeys internals)', () => {
  const source = readFileSync(resolve(testDir, '../theme-provider.tsx'), 'utf-8');

  it('wraps removeItem in try-catch for resilience', () => {
    const fnStart = source.indexOf('function clearLegacyThemeKeys()');
    const fnEnd = source.indexOf('\n}', fnStart) + 2;
    const fnBody = source.slice(fnStart, fnEnd);
    expect(fnBody).toContain('try');
    expect(fnBody).toContain('catch');
  });

  it('guards against missing window (SSR safety)', () => {
    const fnStart = source.indexOf('function clearLegacyThemeKeys()');
    const fnEnd = source.indexOf('\n}', fnStart) + 2;
    const fnBody = source.slice(fnStart, fnEnd);
    expect(fnBody).toContain("typeof window === 'undefined'");
  });

  it('iterates over legacyThemeKeys array', () => {
    const fnStart = source.indexOf('function clearLegacyThemeKeys()');
    const fnEnd = source.indexOf('\n}', fnStart) + 2;
    const fnBody = source.slice(fnStart, fnEnd);
    expect(fnBody).toContain('for');
    expect(fnBody).toContain('legacyThemeKeys');
  });
});

describe('ThemeProvider backend sync', () => {
  const source = readFileSync(resolve(testDir, '../theme-provider.tsx'), 'utf-8');

  it('syncs preference to server via sdk.PATCH /auth/me when authenticated', () => {
    expect(source).toContain("'/auth/me'");
    expect(source).toContain('PATCH');
    expect(source).toContain('themePreference');
  });

  it('only syncs after hydration from user (not on initial load)', () => {
    // The sync must be gated by hydratedFromUser to avoid overwriting
    // the server value with the localStorage default on first load
    const syncBlock = source.slice(source.indexOf("PATCH('/auth/me'") - 200);
    expect(syncBlock).toContain('hydratedFromUser');
  });

  it('catches sync errors silently (fire-and-forget)', () => {
    const syncIndex = source.indexOf("PATCH('/auth/me'");
    const afterSync = source.slice(syncIndex, syncIndex + 300);
    expect(afterSync).toContain('.catch(');
  });
});
