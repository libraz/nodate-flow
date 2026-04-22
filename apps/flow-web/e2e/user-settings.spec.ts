/**
 * User settings sub-routes E2E.
 *
 * Tests /settings/notifications, /settings/security, and
 * /settings/integrations. Each sub-route renders a heading (h1) with
 * the translated section title.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

const SUB_ROUTES = [
  { path: 'notifications', headingPattern: /notification|通知/i },
  { path: 'security', headingPattern: /security|セキュリティ/i },
  { path: 'integrations', headingPattern: /integration|連携/i },
] as const;

test.describe('user settings', () => {
  for (const route of SUB_ROUTES) {
    test(`${route.path} renders heading and content`, async ({ page }) => {
      const { user2: tenant } = loadTenants();
      await injectAuth(page.context(), tenant);

      await page.goto(`/settings/${route.path}`);

      // Verify the section heading is visible
      const heading = page.getByRole('heading', { level: 1 });
      await expect(heading).toBeVisible({ timeout: 10_000 });
      await expect(heading).toHaveText(route.headingPattern);

      // Verify the main content area renders
      const main = page.getByRole('main');
      await expect(main).toBeVisible({ timeout: 5_000 });

      // Verify no i18n key leaks
      const bodyText = await page.locator('body').innerText();
      expect(bodyText).not.toMatch(/\bsettings\.\w+\.title\b/);
      expect(bodyText).not.toMatch(/\bnav\.\w+$/m);

      // Accessibility check
      await checkA11y(page, ['color-contrast', 'region']);
    });
  }
});
