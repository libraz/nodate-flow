/**
 * Inbox page E2E.
 *
 * Happy path:
 *   1. Load pre-seeded tenant and navigate to /inbox.
 *   2. Verify the inbox heading renders.
 *   3. Verify either empty state or notification items are visible.
 *   4. No i18n key leaks, a11y check.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('inbox', () => {
  test('renders inbox page with heading and content', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto('/inbox');

    // The inbox lazy route renders an h1 with t('view.title') from the 'inbox' namespace
    const heading = page.getByRole('heading', { level: 1 });
    await expect(heading).toBeVisible({ timeout: 10_000 });

    // Verify content area is present (either notification items or empty state)
    const mainSection = page.locator('section').first();
    await expect(mainSection).toBeVisible({ timeout: 5_000 });

    // Verify no i18n key leaks
    const bodyText = await page.locator('body').innerText();
    expect(bodyText).not.toMatch(/\bview\.title\b/);
    expect(bodyText).not.toMatch(/\bview\.subtitle\b/);
    expect(bodyText).not.toMatch(/\binbox\.\w+\.\w+/);

    // Accessibility check
    await checkA11y(page, ['color-contrast', 'region']);
  });
});
