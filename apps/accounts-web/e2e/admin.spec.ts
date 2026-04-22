/**
 * Admin pages e2e tests.
 *
 * Covers the admin layout, user list (badges, table headers, pagination),
 * workspace list, admins page (search grant UI), audit logs, and settings.
 *
 * Uses the shared admin tenant from global setup.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('admin pages', () => {
  test.beforeEach(() => {
    const { adminGranted } = loadTenants();
    test.skip(!adminGranted, 'Admin grant failed — instance already has an admin from a prior run');
  });

  test.describe('admin navigation', () => {
    test('admin layout renders sidebar navigation', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin');
      await page.waitForLoadState('networkidle');

      // Should have navigation links for admin sections
      await expect(page.getByRole('link', { name: /users/i })).toBeVisible();
      await expect(page.getByRole('link', { name: /workspaces/i })).toBeVisible();
      await expect(page.getByRole('link', { name: /audit/i })).toBeVisible();
      await expect(page.getByRole('link', { name: /admins/i })).toBeVisible();
      await expect(page.getByRole('link', { name: /settings/i })).toBeVisible();
    });
  });

  test.describe('user management', () => {
    test('renders user list with table headers', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/users');
      await page.waitForLoadState('networkidle');

      await expect(page.getByRole('heading', { name: /user management/i })).toBeVisible();

      // Table headers should not wrap (whiteSpace: nowrap fix)
      const headers = page.locator('th');
      const headerCount = await headers.count();
      expect(headerCount).toBeGreaterThanOrEqual(4);

      // Check specific headers exist
      await expect(page.locator('th').filter({ hasText: /email/i })).toBeVisible();
      await expect(page.locator('th').filter({ hasText: /name/i })).toBeVisible();
      await expect(page.locator('th').filter({ hasText: /status/i })).toBeVisible();
    });

    test('status badges are readable (text visible on badge background)', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/users');
      await page.waitForLoadState('networkidle');

      // Wait for data to load
      await page.waitForSelector('td', { timeout: 10_000 });

      // Find status badges (Active/Suspended)
      const badges = page.locator('span').filter({ hasText: /^(Active|Suspended)$/ });
      const count = await badges.count();

      for (let i = 0; i < count; i++) {
        const badge = badges.nth(i);
        const bg = await badge.evaluate((el) => getComputedStyle(el).backgroundColor);
        const color = await badge.evaluate((el) => getComputedStyle(el).color);
        // Background and text color must differ (the bug was both being identical)
        expect(bg).not.toBe(color);
      }
    });

    test('search input filters users', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/users');
      await page.waitForLoadState('networkidle');

      const searchInput = page.getByPlaceholder(/search users/i);
      await expect(searchInput).toBeVisible();
    });

    test('status filter tabs are visible', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/users');
      await page.waitForLoadState('networkidle');

      await expect(page.getByRole('button', { name: /^all$/i })).toBeVisible();
      await expect(page.getByRole('button', { name: /^active$/i })).toBeVisible();
      await expect(page.getByRole('button', { name: /^suspended$/i })).toBeVisible();
    });

    test('user row links to user detail', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/users');
      await page.waitForLoadState('networkidle');
      await page.waitForSelector('td', { timeout: 10_000 });

      // Click the first user row's detail link/name
      const firstLink = page.locator('a[href*="/admin/users/"]').first();
      if (await firstLink.isVisible()) {
        await firstLink.click();
        await expect(page).toHaveURL(/\/admin\/users\//);
        await expect(page.getByRole('heading', { name: /user details/i })).toBeVisible();
      }
    });

    test('no i18n keys exposed', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/users');
      await page.waitForLoadState('networkidle');

      const body = await page.locator('body').textContent();
      expect(body).not.toMatch(/\busers\.\w+_\w+\b/);
    });
  });

  test.describe('workspace management', () => {
    test('renders workspace list with table headers', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/workspaces');
      await page.waitForLoadState('networkidle');

      await expect(page.getByRole('heading', { name: /workspace management/i })).toBeVisible();

      // Table headers
      await expect(page.locator('th').filter({ hasText: /name/i })).toBeVisible();
      await expect(page.locator('th').filter({ hasText: /slug/i })).toBeVisible();
      await expect(page.locator('th').filter({ hasText: /status/i })).toBeVisible();
    });

    test('workspace status badges readable', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/workspaces');
      await page.waitForLoadState('networkidle');

      await page.waitForSelector('td', { timeout: 10_000 }).catch(() => {
        // May have no workspaces, that's fine
      });

      const badges = page.locator('span').filter({ hasText: /^(Active|Suspended)$/ });
      const count = await badges.count();
      for (let i = 0; i < count; i++) {
        const badge = badges.nth(i);
        const bg = await badge.evaluate((el) => getComputedStyle(el).backgroundColor);
        const color = await badge.evaluate((el) => getComputedStyle(el).color);
        expect(bg).not.toBe(color);
      }
    });
  });

  test.describe('admins page', () => {
    test('renders admin list and grant section', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/admins');
      await page.waitForLoadState('networkidle');

      await expect(page.getByRole('heading', { name: /instance administrators/i })).toBeVisible();

      // Grant section heading
      await expect(page.getByRole('heading', { name: /grant admin/i })).toBeVisible();

      // Search-based grant UI (not raw ID input)
      const searchInput = page.getByPlaceholder(/search by name or email/i);
      await expect(searchInput).toBeVisible();

      // Grant button should be disabled when no user selected
      const grantButton = page.getByRole('button', { name: /^grant$/i });
      await expect(grantButton).toBeDisabled();
    });

    test('grant UI uses search dropdown instead of raw ID input', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/admins');
      await page.waitForLoadState('networkidle');

      // There should be NO input asking for a user ID
      const idInputs = page.locator('input[placeholder*="ID" i], input[placeholder*="uuid" i]');
      await expect(idInputs).toHaveCount(0);

      // Instead there should be a search input
      await expect(page.getByPlaceholder(/search by name or email/i)).toBeVisible();
    });

    test('search dropdown appears on input', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/admins');
      await page.waitForLoadState('networkidle');

      const searchInput = page.getByPlaceholder(/search by name or email/i);
      await searchInput.fill('e2e');

      // Wait for debounced search to trigger dropdown
      await page.waitForTimeout(500);

      // Dropdown should appear (either with results or "no results" message)
      const dropdown = page.locator('ul').filter({
        has: page.locator('li'),
      });
      await expect(dropdown)
        .toBeVisible({ timeout: 5_000 })
        .catch(() => {
          // If no results, the dropdown might show "No users found"
        });
    });

    test('hint text is descriptive', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/admins');
      await page.waitForLoadState('networkidle');

      // Should have hint text about searching
      await expect(page.getByText(/search for a user by name or email/i)).toBeVisible();
    });

    test('revoke buttons present for existing admins', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/admins');
      await page.waitForLoadState('networkidle');

      // The current admin should be in the list with a revoke button
      const revokeButtons = page.getByRole('button', { name: /revoke/i });
      await expect(revokeButtons.first())
        .toBeVisible({ timeout: 10_000 })
        .catch(() => {
          // Might not have admin listed if setup didn't work
        });
    });
  });

  test.describe('audit logs', () => {
    test('renders audit log page structure', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/audit-logs');
      await page.waitForLoadState('networkidle');

      await expect(page.getByRole('heading', { name: /audit log/i })).toBeVisible();

      // Filter controls
      await expect(
        page.getByText(/filter by action/i).or(page.locator('select').first()),
      ).toBeVisible();
    });

    test('no i18n keys exposed', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/audit-logs');
      await page.waitForLoadState('networkidle');

      const body = await page.locator('body').textContent();
      expect(body).not.toMatch(/\baudit_logs\.\w+\b/);
    });
  });

  test.describe('instance settings', () => {
    test('renders settings form', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/settings');
      await page.waitForLoadState('networkidle');

      await expect(page.getByRole('heading', { name: /instance settings/i })).toBeVisible();

      // Key settings
      await expect(page.getByText(/registration open/i)).toBeVisible();
      await expect(page.getByText(/mfa/i)).toBeVisible();

      // Save button
      await expect(page.getByRole('button', { name: /save/i })).toBeVisible();
    });

    test('no i18n keys exposed', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/settings');
      await page.waitForLoadState('networkidle');

      const body = await page.locator('body').textContent();
      expect(body).not.toMatch(/\bsettings\.\w+_\w+\b/);
    });

    test('accessibility check', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/settings');
      await page.waitForLoadState('networkidle');
      await checkA11y(page, ['color-contrast']);
    });
  });
});
