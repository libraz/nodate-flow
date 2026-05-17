/**
 * Theme switching E2E.
 *
 * Documented behaviour (verified from source):
 *
 *   - The theme is set via `<html data-theme="...">`. The provider in
 *     `apps/flow-web/src/providers/theme-provider.tsx` delegates to
 *     `packages/ui/src/providers/theme-provider.tsx` which writes the
 *     attribute via `document.documentElement.setAttribute('data-theme', next)`.
 *   - The picker lives at /settings (redirected to /settings/profile) and is
 *     rendered by `ProfileForm` using the shared `ThemePicker` primitive
 *     (`role="radiogroup" aria-label="Theme"` with `role="radio"` family
 *     buttons + a separate `Color mode` radiogroup).
 *   - Six concrete themes exist: aurora-{dark,light}, glass-{dark,light},
 *     dotline-{dark,light}. The picker exposes 3 family buttons and 3 mode
 *     buttons (light / dark / system); we cycle every (family, mode) pair to
 *     visit all six.
 *   - Persistence: `nf.theme` localStorage key + `PATCH /me` with
 *     `{ themePreference }`. The OpenAPI enum is
 *     `aurora-light,aurora-dark,dotline-light,dotline-dark,glass-light,glass-dark,system`.
 *   - Calendar tokens differ across themes: `--nf-cal-task-color`,
 *     `--nf-cal-task-subtle`, `--nf-cal-color-1` ... `-10`.
 *   - Flash prevention: an inline IIFE in `apps/flow-web/index.html`
 *     reads localStorage and sets `data-theme` BEFORE React renders.
 */

import { type Page, expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { AUTH_API_URL, type TestTenant, injectAuth } from './fixtures/tenant';

type Family = 'aurora' | 'glass' | 'dotline';
type Mode = 'light' | 'dark';
type ThemeId = `${Family}-${Mode}`;

const FAMILY_LABEL: Record<Family, RegExp> = {
  aurora: /^aurora$/i,
  glass: /^glass$/i,
  dotline: /^dotline$/i,
};

const MODE_LABEL: Record<Mode, RegExp> = {
  // The settings UI uses translated labels ("Light"/"Dark" or
  // "ライト"/"ダーク"). Match either to stay locale-agnostic.
  light: /^(light|ライト)$/i,
  dark: /^(dark|ダーク)$/i,
};

const ALL_THEMES: ThemeId[] = [
  'aurora-light',
  'aurora-dark',
  'glass-light',
  'glass-dark',
  'dotline-light',
  'dotline-dark',
];

/**
 * Open /settings/profile for the supplied tenant and wait for the picker
 * to be interactable. Returns the page handle for chaining.
 */
async function openSettings(page: Page, tenant: TestTenant): Promise<void> {
  await injectAuth(page.context(), tenant);
  await page.goto('/settings/profile');
  // Theme radiogroups are rendered by ThemePicker. Wait for both groups
  // (family + color mode) so subsequent clicks aren't racing the lazy
  // settings route mount.
  await expect(page.getByRole('radiogroup', { name: /^theme|^テーマ/i })).toBeVisible({
    timeout: 15_000,
  });
  await expect(
    page.getByRole('radiogroup', { name: /color mode|カラーモード|配色/i }),
  ).toBeVisible();
}

/**
 * Select the given (family, mode) pair via the picker. Mirrors a real
 * user click on the theme card and the mode pill.
 */
async function selectTheme(page: Page, family: Family, mode: Mode): Promise<void> {
  const themeGroup = page.getByRole('radiogroup', { name: /^theme|^テーマ/i });
  await themeGroup.getByRole('radio', { name: FAMILY_LABEL[family] }).click();
  const modeGroup = page.getByRole('radiogroup', { name: /color mode|カラーモード|配色/i });
  await modeGroup.getByRole('radio', { name: MODE_LABEL[mode] }).click();
  // The provider writes data-theme synchronously inside a useEffect, so
  // wait for the attribute to converge before the caller asserts.
  await expect(page.locator('html')).toHaveAttribute('data-theme', `${family}-${mode}`, {
    timeout: 5_000,
  });
}

async function readVar(page: Page, name: string): Promise<string> {
  return page.evaluate(
    (varName) => getComputedStyle(document.documentElement).getPropertyValue(varName).trim(),
    name,
  );
}

test.describe('theme switching', () => {
  test('theme attribute reflects each of the six concrete selections', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await openSettings(page, tenant);

    for (const id of ALL_THEMES) {
      const [family, mode] = id.split('-') as [Family, Mode];
      await selectTheme(page, family, mode);
      // selectTheme already waits, but re-read explicitly so the failure
      // message points at the exact pair the loop is on.
      const attr = await page.locator('html').getAttribute('data-theme');
      expect(attr, `expected data-theme=${id} after picking ${family}+${mode}`).toBe(id);
    }
  });

  test('core color tokens flip between dark and light variants', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await openSettings(page, tenant);

    const TOKENS = [
      '--nf-color-fg',
      '--nf-color-bg',
      '--nf-color-accent',
      '--nf-color-border',
      '--nf-color-success',
    ];

    for (const family of ['aurora', 'glass', 'dotline'] as Family[]) {
      await selectTheme(page, family, 'dark');
      const dark: Record<string, string> = {};
      for (const t of TOKENS) dark[t] = await readVar(page, t);

      await selectTheme(page, family, 'light');
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
    const { user: tenant } = loadTenants();
    await openSettings(page, tenant);

    // --nf-cal-task-color is defined per-theme; dark and light variants
    // of the same family use different oklch lightness values, so the
    // computed value must change. We don't pin a specific oklch literal —
    // any non-empty difference is enough.
    const family: Family = 'aurora';
    await selectTheme(page, family, 'dark');
    const darkVal = await readVar(page, '--nf-cal-task-color');
    expect(darkVal, '--nf-cal-task-color must be defined in aurora-dark').not.toBe('');

    await selectTheme(page, family, 'light');
    const lightVal = await readVar(page, '--nf-cal-task-color');
    expect(lightVal, '--nf-cal-task-color must be defined in aurora-light').not.toBe('');
    expect(
      lightVal,
      `--nf-cal-task-color must differ between dark (${darkVal}) and light (${lightVal})`,
    ).not.toBe(darkVal);

    // Cross-family comparison too: aurora-light vs glass-light should
    // also differ because each family defines its own calendar palette.
    await selectTheme(page, 'glass', 'light');
    const glassLight = await readVar(page, '--nf-cal-task-color');
    expect(glassLight).not.toBe('');
    expect(glassLight, 'aurora-light vs glass-light must differ').not.toBe(lightVal);
  });

  test('persists themePreference on the server via PATCH /me', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await openSettings(page, tenant);

    await selectTheme(page, 'dotline', 'dark');

    // The provider writes localStorage in the same useEffect that sets
    // data-theme, but the server PATCH is fire-and-forget, so poll until
    // the server reflects our pick (or fail loudly).
    const stored = await page.evaluate(() => window.localStorage.getItem('nf.theme'));
    expect(stored).toBe('dotline-dark');

    let serverPref: string | null = null;
    for (let attempt = 0; attempt < 20; attempt++) {
      const res = await fetch(`${AUTH_API_URL}/me`, {
        headers: {
          accept: 'application/json',
          authorization: `Bearer ${tenant.accessToken}`,
        },
      });
      expect(res.ok, `GET /me -> ${res.status}`).toBeTruthy();
      const body = (await res.json()) as { themePreference?: string };
      if (body.themePreference === 'dotline-dark') {
        serverPref = body.themePreference;
        break;
      }
      await new Promise((r) => setTimeout(r, 250));
    }
    expect(serverPref, 'PATCH /me must persist themePreference').toBe('dotline-dark');
  });

  test('no flash on reload — data-theme is set before React mounts', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await openSettings(page, tenant);

    // Pick a non-default theme so the boot script has something to do.
    await selectTheme(page, 'dotline', 'light');

    // Confirm localStorage carries the pick before reload.
    const stored = await page.evaluate(() => window.localStorage.getItem('nf.theme'));
    expect(stored).toBe('dotline-light');

    // `domcontentloaded` fires AFTER the HTML head is parsed (so the
    // inline IIFE in index.html that sets data-theme has executed) but
    // BEFORE the deferred <script type="module"> for React has finished
    // evaluating. Reading data-theme at this point therefore measures
    // the boot script's effect, not React's.
    //
    // Note: `commit` is too early — the document is essentially empty
    // and `<html>` has no children yet, so data-theme reads as null.
    await page.goto('/settings/profile', { waitUntil: 'domcontentloaded' });
    const initial = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
    expect(initial, 'data-theme must be set by inline boot IIFE before React renders').toBe(
      'dotline-light',
    );
    // Defence-in-depth: at DOMContentLoaded the React root must still
    // be empty (or at least not hydrated past the <head> script). The
    // body's #root div should have no children yet because the React
    // bundle is in a <script type="module"> which is deferred.
    const rootHtml = await page.evaluate(() => document.getElementById('root')?.innerHTML ?? null);
    expect(
      rootHtml,
      'React root must not have rendered yet at DOMContentLoaded — otherwise this test does not actually verify pre-React paint',
    ).toBe('');

    // Ensure the React mount converges to the same value rather than
    // overwriting with a default.
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dotline-light', {
      timeout: 10_000,
    });
  });
});
