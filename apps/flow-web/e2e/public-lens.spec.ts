/**
 * Public lens page E2E.
 *
 * Navigates to /public/lenses/invalid-token (no auth required) and
 * verifies the page renders without crashing.
 */

import { expect, test } from '@playwright/test';

test.describe('public lens', () => {
  test('invalid lens token renders a page without crash', async ({ page }) => {
    // No auth injection needed — this is a public route
    await page.goto('/public/lenses/invalid-token');
    await page.waitForLoadState('domcontentloaded');

    // Verify the page rendered (not a blank white screen or JS error)
    const body = page.locator('body');
    await expect(body).not.toBeEmpty();

    // Wait for content to settle
    await page.waitForTimeout(1000);

    // Verify some visible content is present
    const bodyText = await body.innerText();
    expect(bodyText.length).toBeGreaterThan(0);
  });
});
