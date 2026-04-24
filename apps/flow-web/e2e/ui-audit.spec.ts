/**
 * UI audit regression tests for flow-web.
 *
 * Covers issues discovered during the comprehensive UI audit:
 * - Not-found page renders properly (no raw i18n keys)
 * - Today view renders sections (not i18n keys)
 * - Home/index page renders dashboard elements
 * - Login/signup navigation links
 * - No i18n key exposure across main routes
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('not-found page', () => {
  test('renders 404 page with translated content', async ({ page }) => {
    await page.goto('/this-route-does-not-exist-abc123');
    await page.waitForLoadState('networkidle');

    // Should show "404" text (use main region to avoid footer/layout duplicates)
    await expect(page.getByRole('main').getByText('404')).toBeVisible();

    // Should have a "back home" link
    const backLink = page.getByRole('link', { name: /back|home/i });
    await expect(backLink).toBeVisible();

    // No raw i18n keys
    const body = await page.locator('body').textContent();
    expect(body).not.toMatch(/\bnot_found\.\w+\b/);
  });
});

test.describe('login page', () => {
  test('renders login form with signup link', async ({ page }) => {
    await page.goto('/login');
    await page.waitForLoadState('networkidle');

    // Form elements
    await expect(page.getByLabel(/email/i)).toBeVisible();
    await expect(page.getByLabel(/password/i)).toBeVisible();
    await expect(page.getByRole('button', { name: /sign in/i })).toBeVisible();

    // Signup link present
    const signupLink = page.getByRole('link', { name: /sign ?up|create/i });
    await expect(signupLink).toBeVisible();
  });
});

test.describe('signup page', () => {
  test('renders signup form with login link', async ({ page }) => {
    await page.goto('/signup');
    await page.waitForLoadState('networkidle');

    await expect(page.getByLabel(/name/i)).toBeVisible();
    await expect(page.getByLabel(/email/i)).toBeVisible();
    await expect(page.getByLabel(/password/i)).toBeVisible();

    const loginLink = page.getByRole('link', { name: /sign in|log in/i });
    await expect(loginLink).toBeVisible();
  });
});

test.describe('authenticated home page', () => {
  const { user: tenant } = loadTenants();

  test('renders dashboard with greeting and widgets', async ({ page }) => {
    await injectAuth(page.context(), tenant);
    await page.goto('/');
    // networkidle is unusable here: the authenticated shell mounts a
    // long-lived SSE workspace stream that keeps the network busy forever.
    // Wait for the <main> shell + greeting heading instead.
    await expect(page.getByRole('main')).toBeVisible({ timeout: 10_000 });

    // Should show a greeting (personalized or generic)
    const heading = page.getByRole('heading', { level: 1 });
    await expect(heading).toBeVisible({ timeout: 10_000 });

    // No raw i18n keys in page
    const body = await page.locator('body').textContent();
    expect(body).not.toMatch(/\bhome\.\w+_\w+\b/);
    expect(body).not.toMatch(/\blanding\.\w+\b/);
  });

  test('today view renders without i18n key leaks', async ({ page }) => {
    await injectAuth(page.context(), tenant);
    await page.goto('/today');
    // SSE stream prevents networkidle; wait for the /today h1 + shell instead.
    await expect(page.getByRole('main')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible({ timeout: 10_000 });
    // Small settle so any async-loaded section labels (overdue/today/upcoming)
    // finish translating before we scrape the body text.
    await page.waitForTimeout(300);

    const body = await page.locator('body').textContent();
    // Section keys like "today.section_overdue" should be translated
    expect(body).not.toMatch(/\btoday\.\w+_\w+\b/);
  });

  test('accessibility check on home page', async ({ page }) => {
    await injectAuth(page.context(), tenant);
    await page.goto('/');
    // SSE stream prevents networkidle; wait for <main> + h1 to render.
    await expect(page.getByRole('main')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible({ timeout: 10_000 });
    // Small settle so interactive widgets (dock, sidebar) finish mounting
    // before axe walks the tree.
    await page.waitForTimeout(300);
    await checkA11y(page, ['color-contrast', 'region']);
  });
});

test.describe('mobile top-bar layout regression', () => {
  const { user: tenant } = loadTenants();

  /**
   * Regression guard for the mobile top-bar collapsing to ~24px wide.
   *
   * Root cause (fixed in app-shell.module.css): the base `.main` rule was
   * declared *after* the `@media (max-width: 767px)` block, so the mobile
   * `grid-column: 1` override was silently shadowed by the same-specificity
   * `grid-column: 2` declaration below the media query. That forced `<main>`
   * into an implicit second grid column at mobile, collapsed the topBarSlot
   * track to 0px, and pushed the AI cost meter / avatar / notification bell
   * off the left edge with negative `left` coordinates.
   *
   * This test asserts the `<header>` occupies essentially the full viewport
   * width at a mobile viewport (500x812) and that every action-cluster child
   * stays on-screen (left >= 0).
   *
   * Re-regressions this has caught:
   *  - 482e11f ("restore mobile top-bar grid overflow")
   *  - 7a3a4ca ("restructure mobile topbar and label ai cost chip")
   */
  test('top-bar spans the viewport width and keeps actions on-screen at 500px', async ({
    page,
  }) => {
    await page.setViewportSize({ width: 500, height: 812 });
    await injectAuth(page.context(), tenant);
    await page.goto('/today');
    await expect(page.getByRole('main')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 10_000 });
    // Small settle so the topbar children (AI cost meter, notification bell,
    // avatar) finish mounting from their Suspense boundaries.
    await page.waitForTimeout(400);

    // The <main> element must resolve to the first grid column at mobile
    // viewports. If the base `.main { grid-column: 2 }` rule ever moves
    // back below the mobile `@media` block in app-shell.module.css, the
    // same-specificity declaration wins by source order and <main> ends
    // up in an implicit second column — which is the literal fingerprint
    // of this regression.
    const mainGridColumn = await page
      .getByRole('main')
      .evaluate((node) => getComputedStyle(node).gridColumnStart);
    expect(mainGridColumn).toBe('1');

    // The <header> must span ~the full content width. 500px viewport minus
    // a small hamburger-clearance pad (var(--nf-space-16) = 64px inline
    // start) still leaves >= 468px of header width when both sides count.
    const headerBox = await page.getByRole('banner').boundingBox();
    expect(headerBox).not.toBeNull();
    expect(headerBox?.width ?? 0).toBeGreaterThanOrEqual(468);

    // Every direct interactive child of the action cluster (AI cost meter
    // link, inline notification bell button, avatar trigger) must sit at
    // left >= 0. Hidden-at-mobile siblings (theme / lang / sign-out) are
    // not visible and are excluded by the :visible filter.
    const actionChildren = await page
      .locator('header button:visible, header a:visible')
      .evaluateAll((nodes) =>
        nodes.map((node) => {
          const rect = (node as HTMLElement).getBoundingClientRect();
          return { left: rect.left, right: rect.right, width: rect.width };
        }),
      );
    expect(actionChildren.length).toBeGreaterThan(0);
    for (const child of actionChildren) {
      expect(child.left).toBeGreaterThanOrEqual(0);
      expect(child.right).toBeLessThanOrEqual(500);
    }
  });
});

test.describe('i18n key exposure sweep', () => {
  const { user: tenant } = loadTenants();

  const authenticatedRoutes = [
    '/',
    '/today',
    `/projects/${'{projectId}'}`, // will 404 but tests the not-found handling
  ];

  for (const route of authenticatedRoutes) {
    test(`no i18n keys on ${route}`, async ({ page }) => {
      await injectAuth(page.context(), tenant);
      await page.goto(route);
      // networkidle never fires here because the authenticated shell holds an
      // SSE connection open. Wait for <main> + some rendered text instead.
      await expect(page.getByRole('main')).toBeVisible({ timeout: 10_000 });
      await expect
        .poll(async () => ((await page.locator('body').textContent()) ?? '').trim().length, {
          timeout: 10_000,
        })
        .toBeGreaterThan(0);
      await page.waitForTimeout(300);

      const body = await page.locator('body').textContent();
      // Common i18n key patterns: "namespace.key" with dots
      // Should NOT match: "common.loading", "nav.home", etc.
      // Exception: URLs, domain names, version strings
      const suspiciousKeys = body?.match(/\b[a-z]+\.[a-z_]+\.[a-z_]+\b/g) ?? [];
      const filtered = suspiciousKeys.filter(
        (k) =>
          !k.includes('localhost') &&
          !k.includes('example') &&
          !k.match(/^\d/) &&
          !k.includes('.com') &&
          !k.includes('.io') &&
          !k.includes('.js') &&
          !k.includes('.ts'),
      );
      // Allow a small number of false positives (CSS class names, etc.)
      expect(filtered.length).toBeLessThan(5);
    });
  }
});
