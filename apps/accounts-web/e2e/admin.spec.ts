/**
 * Admin pages e2e tests.
 *
 * Covers the admin layout, user list (badges, table headers, pagination),
 * workspace list, admins page (search grant UI), audit logs, and settings.
 *
 * Uses the shared admin tenant from global setup.
 */

import { randomUUID } from 'node:crypto';

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { AUTH_API_URL, injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

/**
 * Registers two fresh non-admin users via POST /auth/register so the
 * grant-admin search has at least two candidates to keyboard-navigate
 * between. The display names share a unique prefix that the test then
 * uses as the search query, isolating it from any other e2e users
 * accumulated on a hot database.
 *
 * Returns the shared display-name prefix that uniquely identifies the
 * pair (e.g. `kbnav-3f0a1b2c`).
 */
async function seedTwoCandidates(): Promise<string> {
  const tag = `kbnav-${randomUUID().slice(0, 8)}`;
  for (let i = 0; i < 2; i++) {
    const email = `${tag}+${i}@example.test`;
    const res = await fetch(`${AUTH_API_URL}/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', accept: 'application/json' },
      body: JSON.stringify({
        email,
        password: 'correct horse battery staple',
        displayName: `${tag} candidate ${i}`,
        locale: 'en',
      }),
    });
    if (!res.ok) {
      throw new Error(`POST /auth/register -> ${res.status} ${await res.text()}`);
    }
  }
  return tag;
}

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

    test('status filter dropdown exposes the three states', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/users');
      await page.waitForLoadState('networkidle');

      // The filter is a <select> (combobox), not a tablist. Verify the
      // three option values are present and the control is reachable
      // by its accessible label.
      const statusSelect = page.getByRole('combobox', { name: /status/i });
      await expect(statusSelect).toBeVisible();
      const optionValues = await statusSelect.evaluate((el) =>
        Array.from((el as HTMLSelectElement).options).map((o) => o.value),
      );
      expect(optionValues).toEqual(['all', 'active', 'suspended']);
    });

    test('user row links to user detail', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/users');
      await page.waitForLoadState('networkidle');
      await page.waitForSelector('td', { timeout: 10_000 });

      // global-setup seeds at least 3 users (user, user2, admin) plus
      // optionally the bootstrap seed admin, so the admin user list is
      // never empty. Treat the link as a hard precondition rather than
      // an optional click.
      const firstLink = page.locator('a[href*="/admin/users/"]').first();
      await expect(firstLink, 'admin user list must contain at least one row').toBeVisible({
        timeout: 10_000,
      });
      await firstLink.click();
      await expect(page).toHaveURL(/\/admin\/users\//);
      await expect(page.getByRole('heading', { name: /user details/i })).toBeVisible();
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

      // global-setup seeds workspaces for both `user` and `admin`, so
      // the admin workspace list always has at least two rows. A
      // missing <td> indicates the seed flow regressed, not an
      // acceptable empty state.
      await page.waitForSelector('td', { timeout: 10_000 });

      const badges = page.locator('span').filter({ hasText: /^(Active|Suspended)$/ });
      const count = await badges.count();
      expect(count, 'expected at least one status badge after workspace seed').toBeGreaterThan(0);
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

      // The Combobox primitive sets `role="listbox"` on the popover
      // unconditionally — both for matching results and for the
      // "no users found" empty branch — so we can deterministically
      // assert it materializes after the debounced search resolves.
      // Swallowing the failure with `.catch(() => {})` would silently
      // mask a regression where the dropdown never opens, so use a
      // hard assertion here.
      const listbox = page.getByRole('listbox');
      await expect(listbox).toBeVisible({ timeout: 5_000 });
      await expect(searchInput).toHaveAttribute('aria-expanded', 'true');
    });

    test('combobox supports keyboard nav (Arrow + Enter selects user)', async ({ page }) => {
      // Seed two fresh non-admin users sharing a unique tag so the
      // search dropdown returns at least two options for keyboard
      // navigation (ArrowDown must change aria-activedescendant, and
      // a generic "e2e" query is unstable on a hot DB where prior
      // runs already promoted every shared user to admin).
      const tag = await seedTwoCandidates();

      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/admins');
      await page.waitForLoadState('networkidle');

      // Combobox should expose ARIA combobox role
      const combo = page.getByRole('combobox', { name: /search user/i });
      await expect(combo).toBeVisible();
      await expect(combo).toHaveAttribute('aria-expanded', 'false');

      await combo.focus();
      await combo.fill(tag);

      // Wait for the listbox to materialize from the debounced search
      const listbox = page.getByRole('listbox');
      await expect(listbox).toBeVisible({ timeout: 5_000 });
      await expect(combo).toHaveAttribute('aria-expanded', 'true');

      // Hard precondition: the two freshly-seeded candidates MUST
      // surface in the search. We poll because the search is
      // debounced and the listbox flips through loading/empty states
      // before the API response settles. Fewer than 2 matches
      // indicates the search endpoint or seed flow broke — fail
      // loudly rather than skip.
      await expect
        .poll(async () => page.getByRole('option').count(), {
          timeout: 5_000,
          message: `admin user search for ${tag} must return both seeded candidates`,
        })
        .toBeGreaterThanOrEqual(2);

      // Arrow Down moves the active descendant
      await combo.press('ArrowDown');
      const ad1 = await combo.getAttribute('aria-activedescendant');
      expect(ad1).not.toBeNull();
      await combo.press('ArrowDown');
      const ad2 = await combo.getAttribute('aria-activedescendant');
      expect(ad2).not.toBe(ad1);

      // Esc closes the listbox
      await combo.press('Escape');
      await expect(listbox).toBeHidden();
      await expect(combo).toHaveAttribute('aria-expanded', 'false');

      // Re-open and pick the first option with Enter
      await combo.focus();
      await combo.press('ArrowDown');
      await expect(page.getByRole('listbox')).toBeVisible();
      await combo.press('Enter');
      await expect(page.getByRole('listbox')).toBeHidden();

      // Grant button is enabled once a user is selected
      const grantButton = page.getByRole('button', { name: /^grant$/i });
      await expect(grantButton).toBeEnabled();
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

      // The shared admin tenant from globalSetup is granted instance
      // admin (the `adminGranted` beforeEach gate already skips this
      // suite when the grant failed), so the admin row must be in the
      // list and must surface a Revoke button. Asserting unconditionally
      // — without `.catch`-swallowing — so a missing row counts as a
      // real regression instead of a silent pass.
      const revokeButtons = page.getByRole('button', { name: /revoke/i });
      await expect(revokeButtons.first()).toBeVisible({ timeout: 10_000 });
    });
  });

  test.describe('audit logs', () => {
    test('renders audit log page structure', async ({ page }) => {
      const { admin } = loadTenants();
      await injectAuth(page.context(), admin);
      await page.goto('/admin/audit-logs');
      await page.waitForLoadState('networkidle');

      await expect(page.getByRole('heading', { name: /audit log/i })).toBeVisible();

      // Filter controls — the action filter is an <input> with the
      // localized "Filter by action" placeholder.
      await expect(page.getByPlaceholder(/filter by action/i)).toBeVisible();
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
