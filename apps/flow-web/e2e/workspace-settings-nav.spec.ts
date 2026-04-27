/**
 * Workspace settings sub-nav navigation E2E.
 *
 * The existing `workspace-settings.spec.ts` only verifies each sub-route
 * renders when reached via direct `goto()`. That misses the sidebar
 * navigation: a regression where the link is broken (wrong `to`,
 * removed key, conditional render bug) ships green because tests jump
 * straight to the URL.
 *
 * This spec lands on `/workspaces/$id/settings/general`, then walks
 * each remaining sidebar entry by clicking its `<Link>` and asserts:
 *   - the URL changes to the expected pathname
 *   - the route's `<main>` content area renders (no white-screen crash)
 *   - the active link gets `aria-current="page"` (visual + a11y signal)
 */

import { expect, test } from '@playwright/test';

import enSettings from '../locales/en/settings.json' with { type: 'json' };
import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

interface NavEntry {
  /** i18n label rendered on the sidebar link. */
  label: string;
  /** URL fragment after `/settings/`. */
  slug: string;
}

const NAV_ENTRIES: readonly NavEntry[] = [
  { label: enSettings.nav.general, slug: 'general' },
  { label: enSettings.nav.data, slug: 'data' },
  { label: enSettings.nav.public_shares, slug: 'public-shares' },
  { label: enSettings.nav.mcp_tokens, slug: 'mcp-tokens' },
  { label: enSettings.nav.webhooks, slug: 'webhooks' },
  { label: enSettings.nav.ai_providers, slug: 'ai-providers' },
  { label: enSettings.nav.ai_agents, slug: 'ai-agents' },
  { label: enSettings.nav.auto_actions, slug: 'auto-actions' },
  { label: enSettings.nav.ai_activity, slug: 'ai-activity' },
  { label: enSettings.nav.ai_metrics, slug: 'ai/metrics' },
  { label: enSettings.nav.weekly_digest, slug: 'weekly-digest' },
];

test.describe('workspace settings sidebar', () => {
  test('every sub-page is reachable by clicking the sidebar link', async ({ page }) => {
    const { user2: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    // Land on the General sub-page so the sidebar is rendered. The
    // settings layout owns the sidebar; without a child route there is
    // no chrome to walk.
    await page.goto(`/workspaces/${tenant.workspaceId}/settings/general`);
    await expect(page.getByRole('main')).toBeVisible({ timeout: 15_000 });

    const sidebar = page.getByRole('navigation', { name: enSettings.sections_label });
    await expect(sidebar).toBeVisible();

    for (const entry of NAV_ENTRIES) {
      const link = sidebar.getByRole('link', { name: entry.label, exact: true });
      await expect(link, `sidebar link missing for ${entry.slug}`).toBeVisible();
      await link.click();

      await expect(page).toHaveURL(
        new RegExp(
          `/workspaces/${tenant.workspaceId}/settings/${entry.slug.replace(/\//g, '\\/')}$`,
        ),
        { timeout: 10_000 },
      );

      // Page renders without a router-level error boundary.
      await expect(page.getByRole('main'), `main missing on ${entry.slug}`).toBeVisible({
        timeout: 10_000,
      });

      // The active link is aria-current=page so screen readers and the
      // visual highlight stay in sync.
      await expect(link).toHaveAttribute('aria-current', 'page');
    }
  });
});
