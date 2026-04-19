/**
 * Smoke test verifying that time-web's main.tsx wraps the app with the
 * required provider hierarchy: I18nProvider, ThemeProvider, ConfirmProvider (C3).
 *
 * Uses source-code analysis to verify the provider nesting order without
 * bootstrapping the full router or network layer.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('time-web main.tsx provider hierarchy', () => {
  const source = readFileSync(resolve(__dirname, '../main.tsx'), 'utf-8');

  it('imports I18nProvider', () => {
    expect(source).toContain("import { I18nProvider } from './providers/i18n-provider'");
  });

  it('imports ThemeProvider', () => {
    expect(source).toContain("import { ThemeProvider } from './providers/theme-provider'");
  });

  it('imports ConfirmProvider', () => {
    expect(source).toContain("import ConfirmProvider from '@nodate-flow/ui/primitives/confirm'");
  });

  it('wraps in StrictMode', () => {
    expect(source).toContain('<StrictMode>');
    expect(source).toContain('</StrictMode>');
  });

  it('wraps in ErrorBoundary', () => {
    expect(source).toContain('<ErrorBoundary');
  });

  it('nests I18nProvider outside QueryProvider', () => {
    const i18nOpen = source.indexOf('<I18nProvider>');
    const queryOpen = source.indexOf('<QueryProvider>');
    expect(i18nOpen).toBeGreaterThan(-1);
    expect(queryOpen).toBeGreaterThan(-1);
    expect(i18nOpen).toBeLessThan(queryOpen);
  });

  it('nests QueryProvider outside ThemeProvider', () => {
    const queryOpen = source.indexOf('<QueryProvider>');
    const themeOpen = source.indexOf('<ThemeProvider>');
    expect(queryOpen).toBeGreaterThan(-1);
    expect(themeOpen).toBeGreaterThan(-1);
    expect(queryOpen).toBeLessThan(themeOpen);
  });

  it('includes ConfirmProvider inside ThemeProvider', () => {
    const themeOpen = source.indexOf('<ThemeProvider>');
    const themeClose = source.indexOf('</ThemeProvider>');
    const confirmOpen = source.indexOf('<ConfirmProvider');
    expect(confirmOpen).toBeGreaterThan(themeOpen);
    expect(confirmOpen).toBeLessThan(themeClose);
  });

  it('includes RouterProvider inside ThemeProvider', () => {
    const themeOpen = source.indexOf('<ThemeProvider>');
    const themeClose = source.indexOf('</ThemeProvider>');
    const routerOpen = source.indexOf('<RouterProvider');
    expect(routerOpen).toBeGreaterThan(themeOpen);
    expect(routerOpen).toBeLessThan(themeClose);
  });
});
