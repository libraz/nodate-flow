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

import enInbox from '../locales/en/inbox.json' with { type: 'json' };
import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

const copy = {
  empty: enInbox.view.empty,
} as const;

test.describe('inbox', () => {
  test('renders inbox page with heading and content', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto('/inbox');

    // The inbox lazy route renders an h1 with t('view.title') from the 'inbox' namespace
    const heading = page.getByRole('heading', { level: 1 });
    await expect(heading).toBeVisible({ timeout: 10_000 });

    // The inbox surface always resolves to one of two visible states:
    //   1. an empty-state card carrying the localized "Your inbox is
    //      empty" copy (when filteredItems.length === 0), OR
    //   2. a <ul> whose first <li> renders an InboxItemRow.
    // Assert exactly one of these is visible so the test fails loudly
    // if the page renders neither (e.g. a Suspense boundary stalls or
    // the list query throws). `expect.poll` lets us race both branches
    // without flakiness from initial loading frames.
    const emptyState = page.getByText(copy.empty, { exact: true });
    const firstItem = page.locator('main ul > li').first();
    await expect
      .poll(
        async () => {
          const [emptyVisible, itemVisible] = await Promise.all([
            emptyState.isVisible(),
            firstItem.isVisible(),
          ]);
          return emptyVisible || itemVisible;
        },
        { timeout: 10_000 },
      )
      .toBe(true);

    // Verify no i18n key leaks
    const bodyText = await page.locator('body').innerText();
    expect(bodyText).not.toMatch(/\bview\.title\b/);
    expect(bodyText).not.toMatch(/\bview\.subtitle\b/);
    expect(bodyText).not.toMatch(/\binbox\.\w+\.\w+/);

    // Accessibility check
    await checkA11y(page, ['color-contrast', 'region']);
  });
});
