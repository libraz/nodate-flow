/**
 * Command palette E2E.
 *
 * keyboard.spec.ts already covers the pure open/close behaviour. This spec
 * exercises an actual palette action:
 *   1. Open the palette via Cmd+K / Ctrl+K from the authenticated root.
 *   2. Type a substring of the localized "Today" / "My tasks" navigation
 *      label so the palette filters to that single row.
 *   3. Activate the highlighted row with Enter.
 *   4. Assert the URL changed to /today AND the /today landmark (main +
 *      h1 today.title) is visible.
 *
 * The shared tenant is used because we only read (navigate). No data is
 * created, so createTestTenant() is not required.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

/**
 * Per-locale palette row label, typed query, and /today heading. The
 * palette filter is a case-insensitive `includes` against the translated
 * `nav.today` key, which is "My tasks" in EN and "今日のタスク" in JA.
 */
interface LocaleStrings {
  rowLabel: RegExp;
  query: string;
  heading: RegExp;
}

const TODAY_STRINGS = {
  en: { rowLabel: /^My tasks$/, query: 'my tasks', heading: /^My tasks$/ },
  ja: { rowLabel: /^今日のタスク$/, query: '今日のタスク', heading: /^今日のタスク$/ },
} as const satisfies Record<string, LocaleStrings>;

function pickLocale(lang: string | null): LocaleStrings {
  if (lang === 'ja') return TODAY_STRINGS.ja;
  return TODAY_STRINGS.en;
}

test.describe('command palette', () => {
  test('Cmd+K -> type -> Enter navigates to the matching route', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto('/');

    // Wait for the authenticated shell to render. networkidle is not safe
    // here because the SSE workspace stream keeps the network busy.
    await expect(page.getByRole('main')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible({ timeout: 10_000 });

    // Detect the active language so we can assert against the right copy.
    const lang = await page.locator('html').getAttribute('lang');
    const { rowLabel, query, heading: headingPattern } = pickLocale(lang);

    // Short settle so the Cmd+K keydown listener is wired.
    await page.waitForTimeout(300);

    const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
    await page.keyboard.press(`${modifier}+k`);

    const palette = page.getByRole('dialog', { name: /command/i });
    await expect(palette).toBeVisible({ timeout: 5_000 });

    // Type the localized query. The palette input is auto-focused.
    await page.keyboard.type(query);

    // The matching row should filter down to exactly one Today entry.
    const todayRow = palette.getByRole('button', { name: rowLabel });
    await expect(todayRow).toBeVisible({ timeout: 3_000 });

    await page.keyboard.press('Enter');

    // URL changed and the /today landmark is rendered.
    await expect(page).toHaveURL(/\/today(\?|$)/, { timeout: 5_000 });
    await expect(palette).not.toBeVisible({ timeout: 3_000 });

    const main = page.getByRole('main');
    await expect(main).toBeVisible({ timeout: 10_000 });
    await expect(main.getByRole('heading', { level: 1, name: headingPattern })).toBeVisible({
      timeout: 10_000,
    });
  });
});
