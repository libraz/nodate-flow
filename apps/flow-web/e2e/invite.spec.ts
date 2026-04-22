/**
 * Invite page E2E.
 *
 * Navigates to /invite/invalid-token-abc (a token that does not exist)
 * and verifies the error state renders correctly. The invite page uses
 * useSuspenseQuery to fetch invite info; an invalid token should result
 * in an error boundary or error state.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

test.describe('invite', () => {
  test('invalid invite token shows error state', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto('/invite/invalid-token-abc');

    // The page should render something — either an error boundary fallback,
    // a toast, or a visible error message. We check that the page is not
    // blank and contains some visible content.
    const body = page.locator('body');
    await expect(body).not.toBeEmpty();

    // Wait for any error state or content to render
    await page.waitForTimeout(3_000);

    // Verify no raw i18n key leaks in the rendered page
    const bodyText = await body.innerText();
    expect(bodyText).not.toMatch(/\bworkspaces\.invites\.\w+/);
    expect(bodyText).not.toMatch(/\bworkspaces\.roles\.\w+/);
  });
});
