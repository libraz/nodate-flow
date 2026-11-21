/**
 * Project archive E2E: archive from the project settings tab (toast with
 * Undo), the archived banner above the tabs, the archived-projects section
 * on the Archive page, and the command palette candidates.
 */

import { expect, type Page, test } from '@playwright/test';

import enArchive from '../locales/en/archive.json' with { type: 'json' };
import enCommon from '../locales/en/common.json' with { type: 'json' };
import { cleanupTenant, createTestTenant, injectAuth, type TestTenant } from './fixtures/tenant';

const copy = {
  archive: enCommon.projects.archive.action,
  restore: enCommon.projects.archive.restore,
  undo: enCommon.common.undo,
  banner: enCommon.projects.archived_banner.text,
  settingsTab: enCommon.projects.detail.tabs.settings,
  archivedProjects: enArchive.projects.title,
  history: enCommon.nav.history,
} as const;

function projectName(tenant: TestTenant): string {
  return `E2E Project ${tenant.projectSlug.replace(/^prj-/, '')}`;
}

function done(name: string): string {
  return enCommon.projects.archive.done.replace('{name}', name);
}

function restored(name: string): string {
  return enCommon.projects.archive.restored.replace('{name}', name);
}

async function openSettings(page: Page, tenant: TestTenant): Promise<void> {
  await page.goto(`/workspaces/${tenant.workspaceId}/projects/${tenant.projectId}?tab=settings`);
  await expect(page.getByRole('button', { name: copy.archive, exact: true })).toBeVisible({
    timeout: 15_000,
  });
}

async function paletteHasProject(page: Page, name: string): Promise<boolean> {
  const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
  await page.keyboard.press(`${modifier}+k`);
  const palette = page.getByRole('dialog', { name: /command/i });
  await expect(palette).toBeVisible({ timeout: 5_000 });
  const input = palette.locator('input').first();
  await input.fill(name);
  // The "Create new task" row for a single project is named after it, so
  // give the filter a moment before reading the project row.
  await page.waitForTimeout(500);
  const visible = await palette.getByRole('button', { name, exact: true }).isVisible();
  await input.press('Escape');
  await expect(palette).toBeHidden();
  return visible;
}

test.describe('project archive', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;
  test.afterEach(async () => {
    if (tenant) await cleanupTenant(tenant);
    tenant = null;
  });

  test('archive with undo from the settings tab', async ({ page }) => {
    tenant = await createTestTenant();
    const name = projectName(tenant);
    await injectAuth(page.context(), tenant);
    await openSettings(page, tenant);

    await page.getByRole('button', { name: copy.archive, exact: true }).click();
    await expect(page.getByText(done(name))).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText(copy.banner)).toBeVisible({ timeout: 10_000 });

    await page.getByRole('button', { name: copy.undo, exact: true }).click();
    await expect(page.getByText(restored(name))).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText(copy.banner)).toBeHidden();
    await expect(page.getByRole('button', { name: copy.archive, exact: true })).toBeVisible();
  });

  test('banner sits above the tabs and its restore button brings the project back', async ({
    page,
  }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);
    await openSettings(page, tenant);
    await page.getByRole('button', { name: copy.archive, exact: true }).click();

    const banner = page.getByText(copy.banner);
    await expect(banner).toBeVisible({ timeout: 10_000 });
    const tab = page.getByRole('tab', { name: copy.settingsTab, exact: true });
    const bannerBox = await banner.boundingBox();
    const tabBox = await tab.boundingBox();
    expect(bannerBox?.y ?? Number.POSITIVE_INFINITY).toBeLessThan(
      tabBox?.y ?? Number.NEGATIVE_INFINITY,
    );

    // Reload so the restore comes from the banner, not the toast.
    await page.reload();
    await expect(banner).toBeVisible({ timeout: 15_000 });
    await page.getByRole('button', { name: copy.restore, exact: true }).first().click();
    await expect(banner).toBeHidden({ timeout: 10_000 });
    await expect(page.getByRole('button', { name: copy.archive, exact: true })).toBeVisible();
  });

  test('archived project leaves the sidebar, is listed on the Archive page and restores from there', async ({
    page,
  }) => {
    tenant = await createTestTenant();
    const name = projectName(tenant);
    const sidebar = page.getByRole('navigation', { name: enCommon.nav.primary });
    await injectAuth(page.context(), tenant);
    await openSettings(page, tenant);
    await expect(sidebar.getByRole('link', { name, exact: true })).toBeVisible({
      timeout: 15_000,
    });
    await page.getByRole('button', { name: copy.archive, exact: true }).click();
    await expect(page.getByText(copy.banner)).toBeVisible({ timeout: 10_000 });
    await expect(sidebar.getByRole('link', { name, exact: true })).toBeHidden({
      timeout: 10_000,
    });

    await page.goto(`/workspaces/${tenant.workspaceId}/tasks/archived`);
    const section = page.getByRole('region', { name: copy.archivedProjects });
    await expect(section).toBeVisible({ timeout: 15_000 });
    await expect(section.getByRole('link', { name, exact: true })).toBeVisible();
    await expect(sidebar.getByRole('link', { name, exact: true })).toBeHidden();

    await section.getByRole('button', { name: copy.restore, exact: true }).click();
    await expect(section).toBeHidden({ timeout: 10_000 });
    await expect(page.getByText(restored(name))).toBeVisible();
    await expect(sidebar.getByRole('link', { name, exact: true })).toBeVisible({
      timeout: 10_000,
    });
  });

  test('archived project drops out of the command palette and returns on restore', async ({
    page,
  }) => {
    tenant = await createTestTenant();
    const name = projectName(tenant);
    await injectAuth(page.context(), tenant);
    await page.goto('/');
    await expect(page.getByRole('main')).toBeVisible({ timeout: 15_000 });
    await page.waitForTimeout(300);
    expect(await paletteHasProject(page, name)).toBe(true);

    await openSettings(page, tenant);
    await page.getByRole('button', { name: copy.archive, exact: true }).click();
    await expect(page.getByText(copy.banner)).toBeVisible({ timeout: 10_000 });
    expect(await paletteHasProject(page, name)).toBe(false);

    await page.getByRole('button', { name: copy.undo, exact: true }).click();
    await expect(page.getByText(copy.banner)).toBeHidden({ timeout: 10_000 });
    expect(await paletteHasProject(page, name)).toBe(true);
  });

  test('sidebar History entry is current on both /timeline and /activity', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);
    for (const path of ['timeline', 'activity']) {
      await page.goto(`/workspaces/${tenant.workspaceId}/${path}`);
      await expect(
        page
          .getByRole('navigation', { name: enCommon.nav.primary })
          .getByRole('link', { name: copy.history, exact: true }),
      ).toHaveAttribute('aria-current', 'page', { timeout: 15_000 });
    }
  });
});
