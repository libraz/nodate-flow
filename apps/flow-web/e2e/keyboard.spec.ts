/**
 * Keyboard shortcut E2E.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST.
 *   2. Inject auth and navigate to the authenticated root.
 *   3. Press Cmd+K (or Ctrl+K on Linux) and verify the command palette opens.
 *   4. Press Escape and verify the command palette closes.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('keyboard shortcuts', () => {
  test('Cmd+K opens command palette, Escape closes it', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto('/');
    await expect(page).toHaveURL(/\//, { timeout: 10_000 });

    // Wait for the app to fully hydrate
    await page.waitForLoadState('networkidle');

    // Accessibility check on the main authenticated view
    await checkA11y(page, ['color-contrast', 'region']);

    // The command palette should not be visible initially
    const palette = page
      .getByRole('dialog', { name: /command/i })
      .or(page.locator('[data-testid="command-palette"]'));
    await expect(palette).not.toBeVisible();

    // Open command palette with Cmd+K (Meta+K on macOS, Control+K elsewhere)
    const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
    await page.keyboard.press(`${modifier}+k`);

    // Verify command palette is visible
    await expect(palette).toBeVisible({ timeout: 5_000 });

    // Close with Escape
    await page.keyboard.press('Escape');

    // Verify command palette is hidden
    await expect(palette).not.toBeVisible({ timeout: 3_000 });
  });
});
