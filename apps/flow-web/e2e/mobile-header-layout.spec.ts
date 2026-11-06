import { type BoundingBox, expect, test } from '@playwright/test';

import {
  AUTH_API_URL,
  cleanupTenant,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

const LONG_WORKSPACE_NAME =
  'Operations Workspace With A Long Name That Must Be Truncated Without Covering Header Controls';
const VIEWPORT_WIDTHS = [375, 390] as const;

function overlaps(left: BoundingBox, right: BoundingBox): boolean {
  return (
    left.x < right.x + right.width &&
    left.x + left.width > right.x &&
    left.y < right.y + right.height &&
    left.y + left.height > right.y
  );
}

function requireBox(box: BoundingBox | null, name: string): BoundingBox {
  expect(box, `${name} should have a visible box`).not.toBeNull();
  if (box === null) throw new Error(`${name} should have a visible box`);
  return box;
}

async function renameWorkspace(tenant: TestTenant): Promise<void> {
  const response = await fetch(`${AUTH_API_URL}/workspaces/${tenant.workspaceId}`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ name: LONG_WORKSPACE_NAME }),
  });
  if (!response.ok) {
    throw new Error(`PATCH /workspaces/{wsId} -> ${response.status} ${await response.text()}`);
  }
}

for (const locale of ['en', 'ja'] as const) {
  test(`mobile top-bar controls do not overlap a long ${locale} workspace breadcrumb`, async ({
    page,
  }) => {
    const tenant = await createTestTenant({ locale });
    try {
      await renameWorkspace(tenant);
      await injectAuth(page.context(), tenant);

      const viewport = { height: 812 };
      const breadcrumbName = locale === 'ja' ? 'パンくずリスト' : 'Breadcrumb';
      const breadcrumb = page.getByRole('navigation', { name: breadcrumbName });
      const searchButton = page.locator('[data-search-trigger]');
      const aiCostMeter = page.locator('header a[href*="/settings/ai-activity"]');
      const avatarButton = page.locator('header button[aria-haspopup="menu"]');
      const rightCluster = page.locator('header > div').last();

      for (const width of VIEWPORT_WIDTHS) {
        await page.setViewportSize({ ...viewport, width });
        await page.goto(`/workspaces/${tenant.workspaceId}/settings/general`);
        await expect(page.getByRole('main')).toBeVisible({ timeout: 15_000 });
        await expect(breadcrumb).toBeVisible({ timeout: 15_000 });
        await expect(breadcrumb).toContainText(LONG_WORKSPACE_NAME);
        await expect(searchButton).toBeVisible();
        await expect(aiCostMeter).toBeVisible({ timeout: 15_000 });
        await expect(avatarButton).toBeVisible();
        await expect(rightCluster).toBeVisible();

        const breadcrumbBox = requireBox(await breadcrumb.boundingBox(), 'breadcrumb');
        const searchBox = requireBox(await searchButton.boundingBox(), 'search button');
        const aiBox = requireBox(await aiCostMeter.boundingBox(), 'AI cost meter');
        const avatarBox = requireBox(await avatarButton.boundingBox(), 'avatar button');
        const rightBox = requireBox(await rightCluster.boundingBox(), 'right action cluster');

        expect(breadcrumbBox.width).toBeGreaterThan(0);
        expect(searchBox.width).toBeGreaterThanOrEqual(32);
        expect(avatarBox.width).toBeGreaterThanOrEqual(32);
        expect(overlaps(breadcrumbBox, searchBox), 'breadcrumb overlaps search').toBe(false);
        expect(overlaps(breadcrumbBox, aiBox), 'breadcrumb overlaps AI cost meter').toBe(false);
        expect(overlaps(breadcrumbBox, rightBox), 'breadcrumb overlaps right action cluster').toBe(
          false,
        );
        expect(overlaps(searchBox, rightBox), 'search overlaps right action cluster').toBe(false);
        expect(aiBox.x + aiBox.width).toBeLessThanOrEqual(width);
        expect(avatarBox.x + avatarBox.width).toBeLessThanOrEqual(width);
        expect(rightBox.x + rightBox.width).toBeLessThanOrEqual(width);
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
          'document should not scroll horizontally',
        ).toBe(true);
      }
    } finally {
      await cleanupTenant(tenant);
    }
  });
}
