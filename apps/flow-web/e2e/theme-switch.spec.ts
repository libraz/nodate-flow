/**
 * Theme E2E.
 *
 * The theme picker lives in accounts-web; flow-web only applies the result.
 * The preference is changed with `PATCH /me { themePreference }` and the
 * test asserts what flow-web renders after loading:
 *
 *   - `<html data-theme>` converges to the stored concrete theme (the
 *     provider hydrates from `GET /me` over whatever localStorage holds).
 *   - `system` follows the emulated OS colour scheme.
 *   - Colour and calendar tokens differ between dark and light variants.
 *   - An inline boot script in `index.html` sets `data-theme` from
 *     localStorage before React mounts (no flash on reload).
 *
 * Every test owns a fresh tenant so the PATCH cannot leak into other specs.
 */

import { expect, type Page, test } from '@playwright/test';

import {
  AUTH_API_URL,
  cleanupTenant,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

type Family = 'glass' | 'modern' | 'washi' | 'classic' | 'nothing';
type Mode = 'light' | 'dark';
type ThemeId = `${Family}-${Mode}`;

const FAMILIES: readonly Family[] = ['glass', 'modern', 'washi', 'classic', 'nothing'];

const ALL_THEMES: ThemeId[] = FAMILIES.flatMap((f) => [`${f}-light`, `${f}-dark`] as ThemeId[]);

async function patchThemePreference(tenant: TestTenant, themePreference: string): Promise<void> {
  const res = await fetch(`${AUTH_API_URL}/me`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ themePreference }),
  });
  if (!res.ok) {
    throw new Error(`PATCH /me ${themePreference} -> ${res.status} ${await res.text()}`);
  }
  const body = (await res.json()) as { themePreference?: string };
  expect(body.themePreference, 'PATCH /me must echo the stored preference').toBe(themePreference);
}

/**
 * Stores `theme` on the server, loads flow-web and waits for data-theme to converge.
 *
 * When the stored local preference differs from the server's, the provider
 * hydrates and writes the value back with its own `PATCH /me`. That request
 * is awaited so it cannot land after the next test step's PATCH.
 */
async function loadWithTheme(page: Page, tenant: TestTenant, theme: string): Promise<void> {
  const previous = page.url().startsWith('http')
    ? await page.evaluate(() => window.localStorage.getItem('nf.theme'))
    : null;
  await patchThemePreference(tenant, theme);
  const writeBack =
    previous === theme
      ? Promise.resolve(null)
      : page.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().endsWith('/me'), {
          timeout: 15_000,
        });
  await page.goto('/settings/notifications');
  await writeBack;
  await expect(page.locator('#main-content').first()).toBeVisible({ timeout: 15_000 });
  await expect(page.locator('html')).toHaveAttribute('data-theme', theme, { timeout: 10_000 });
}

async function readVar(page: Page, name: string): Promise<string> {
  return page.evaluate(
    (varName) => getComputedStyle(document.documentElement).getPropertyValue(varName).trim(),
    name,
  );
}

test.describe('theme', () => {
  let tenant: TestTenant;
  test.beforeEach(async ({ page }) => {
    tenant = await createTestTenant({ locale: 'en' });
    await injectAuth(page.context(), tenant);
  });
  test.afterEach(async () => {
    await cleanupTenant(tenant);
  });

  test('data-theme reflects each of the ten concrete preferences stored via PATCH /me', async ({
    page,
  }) => {
    test.setTimeout(120_000);
    for (const id of ALL_THEMES) {
      await loadWithTheme(page, tenant, id);
      const attr = await page.locator('html').getAttribute('data-theme');
      expect(attr, `expected data-theme=${id} after PATCH /me`).toBe(id);
    }
  });

  test('system preference follows the OS colour scheme', async ({ page }) => {
    await page.emulateMedia({ colorScheme: 'dark' });
    await patchThemePreference(tenant, 'system');
    await page.goto('/settings/notifications');
    await expect(page.locator('#main-content').first()).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('html')).toHaveAttribute('data-theme', /-dark$/, { timeout: 10_000 });

    await page.emulateMedia({ colorScheme: 'light' });
    await expect(page.locator('html')).toHaveAttribute('data-theme', /-light$/, {
      timeout: 10_000,
    });
  });

  test('core color tokens flip between dark and light variants', async ({ page }) => {
    test.setTimeout(120_000);
    // Only the roles that must invert; classic and nothing keep one accent and success across modes.
    const TOKENS = ['--nf-color-fg', '--nf-color-bg', '--nf-color-border'];

    for (const family of FAMILIES) {
      await loadWithTheme(page, tenant, `${family}-dark`);
      const dark: Record<string, string> = {};
      for (const t of TOKENS) dark[t] = await readVar(page, t);

      await loadWithTheme(page, tenant, `${family}-light`);
      for (const t of TOKENS) {
        const lightVal = await readVar(page, t);
        expect(lightVal, `${t} must be defined for ${family}-light`).not.toBe('');
        expect(
          lightVal,
          `${t} must differ between ${family}-dark (${dark[t]}) and ${family}-light (${lightVal})`,
        ).not.toBe(dark[t]);
      }
    }
  });

  test('calendar tokens flip across themes', async ({ page }) => {
    await loadWithTheme(page, tenant, 'washi-dark');
    const darkVal = await readVar(page, '--nf-cal-task-color');
    expect(darkVal, '--nf-cal-task-color must be defined in washi-dark').not.toBe('');

    await loadWithTheme(page, tenant, 'washi-light');
    const lightVal = await readVar(page, '--nf-cal-task-color');
    expect(lightVal, '--nf-cal-task-color must be defined in washi-light').not.toBe('');
    expect(lightVal, 'washi-dark vs washi-light must differ').not.toBe(darkVal);

    await loadWithTheme(page, tenant, 'glass-light');
    const glassLight = await readVar(page, '--nf-cal-task-color');
    expect(glassLight).not.toBe('');
    expect(glassLight, 'washi-light vs glass-light must differ').not.toBe(lightVal);
  });

  test('no flash on reload: data-theme is set before React mounts', async ({ page }) => {
    await loadWithTheme(page, tenant, 'washi-light');

    // The provider mirrors the applied theme into localStorage.
    await expect
      .poll(() => page.evaluate(() => window.localStorage.getItem('nf.theme')), { timeout: 5_000 })
      .toBe('washi-light');

    // `domcontentloaded` fires after the head's inline boot script ran but
    // before the deferred module bundle has rendered, so data-theme read here
    // is the boot script's doing, not React's.
    await page.goto('/settings/notifications', { waitUntil: 'domcontentloaded' });
    const initial = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
    expect(initial, 'data-theme must be set by the inline boot script before React renders').toBe(
      'washi-light',
    );
    const rootHtml = await page.evaluate(() => document.getElementById('root')?.innerHTML ?? null);
    expect(rootHtml, 'React root must still be empty at DOMContentLoaded').toBe('');

    await expect(page.locator('html')).toHaveAttribute('data-theme', 'washi-light', {
      timeout: 10_000,
    });
  });
});
