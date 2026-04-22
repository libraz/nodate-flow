/**
 * Pages (wiki) E2E.
 *
 * Happy path:
 *   1. Load pre-seeded tenant and navigate to /pages.
 *   2. Verify the pages layout renders (search bar, tree sidebar, content area).
 *   3. Verify no i18n key leaks, a11y check.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('pages', () => {
  test('renders pages list with search and content area', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto('/pages');

    // The PageList component renders a search input and a two-pane layout
    const searchInput = page.getByRole('searchbox');
    await expect(searchInput).toBeVisible({ timeout: 10_000 });

    // Verify the main section container renders
    const section = page.locator('section').first();
    await expect(section).toBeVisible({ timeout: 5_000 });

    // Verify no i18n key leaks (pages namespace uses keys like "empty", "search_placeholder")
    const bodyText = await page.locator('body').innerText();
    expect(bodyText).not.toMatch(/\bpages\.empty\b/);
    expect(bodyText).not.toMatch(/\bpages\.search_placeholder\b/);

    // Accessibility check
    await checkA11y(page, ['color-contrast', 'region', 'page-has-heading-one']);
  });
});
