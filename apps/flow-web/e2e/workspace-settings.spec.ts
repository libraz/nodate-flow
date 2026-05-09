/**
 * Workspace settings sub-routes E2E.
 *
 * Iterates over each workspace settings sub-page and verifies:
 *   - The page renders content (not blank)
 *   - No i18n key leaks
 *   - a11y check passes
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

const SUB_ROUTES = [
  'general',
  'mcp-tokens',
  'ai-providers',
  'ai-agents',
  'auto-actions',
  'ai-activity',
  'weekly-digest',
  'audit-log',
] as const;

test.describe('workspace settings', () => {
  for (const subPath of SUB_ROUTES) {
    test(`${subPath} renders content`, async ({ page }) => {
      const { user2: tenant } = loadTenants();
      await injectAuth(page.context(), tenant);

      await page.goto(`/workspaces/${tenant.workspaceId}/settings/${subPath}`);
      await page.waitForLoadState('domcontentloaded');

      // Verify the main content area renders
      const main = page.getByRole('main');
      await expect(main).toBeVisible({ timeout: 15_000 });
      await expect(main.getByRole('heading', { level: 1 }).first()).toBeVisible({
        timeout: 15_000,
      });

      // Verify no i18n key leaks
      const bodyText = await page.locator('body').innerText();
      expect(bodyText).not.toMatch(/\bsections_label\b/);

      // Accessibility check
      await checkA11y(page, ['color-contrast', 'region', 'heading-order']);
    });
  }
});
