/**
 * User settings sub-routes E2E.
 *
 * Covers /settings/notifications and /settings/integrations. Each renders
 * an h1 with the translated section title. Profile and security moved to
 * accounts-web (see settings.spec.ts for the links).
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

const SUB_ROUTES = [
  { path: 'notifications', headingPattern: /notification|通知/i },
  { path: 'integrations', headingPattern: /integration|連携/i },
] as const;

test.describe('user settings', () => {
  for (const route of SUB_ROUTES) {
    test(`${route.path} renders heading and content`, async ({ page }) => {
      const { user2: tenant } = loadTenants();
      await injectAuth(page.context(), tenant);

      await page.goto(`/settings/${route.path}`);

      const heading = page.getByRole('heading', { level: 1 });
      await expect(heading).toBeVisible({ timeout: 10_000 });
      await expect(heading).toHaveText(route.headingPattern);

      await expect(page.getByRole('main')).toBeVisible({ timeout: 5_000 });

      // No i18n key leaks
      const bodyText = await page.locator('body').innerText();
      expect(bodyText).not.toMatch(/\bsettings\.\w+\.title\b/);
      expect(bodyText).not.toMatch(/\bnav\.\w+$/m);

      await checkA11y(page, ['color-contrast', 'region']);
    });
  }
});
