/**
 * Workspace timeline E2E.
 *
 * Happy path:
 *   1. Load pre-seeded tenant and navigate to /workspaces/{id}/timeline.
 *   2. Verify the timeline heading renders.
 *   3. No i18n key leaks, a11y check.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('workspace timeline', () => {
  test('renders timeline view with heading', async ({ page }) => {
    const { user2: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto(`/workspaces/${tenant.workspaceId}/timeline`);

    // The timeline lazy route renders an h1 with "Timeline"
    const heading = page.getByRole('heading', { name: /timeline/i });
    await expect(heading).toBeVisible({ timeout: 10_000 });

    // Verify no i18n key leaks
    const bodyText = await page.locator('body').innerText();
    expect(bodyText).not.toMatch(/\btimeline\.view\.title\b/);
    expect(bodyText).not.toMatch(/\bcommon\.loading\b/);

    // Accessibility check
    await checkA11y(page, ['color-contrast', 'region', 'heading-order']);
  });
});
