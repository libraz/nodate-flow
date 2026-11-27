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

import { expect, type Page, test } from '@playwright/test';

import enSettings from '../locales/en/settings.json' with { type: 'json' };
import { loadTenants } from './fixtures/load-tenants';
import { cleanupTenant, createMember, createTestTenant, injectAuth } from './fixtures/tenant';

interface NavEntry {
  /** i18n label rendered on the sidebar link. */
  label: string;
  /** URL fragment after `/settings/` that the link navigates to (after any redirect). */
  slug: string;
}

const NAV_ENTRIES: readonly NavEntry[] = [
  { label: enSettings.nav.general, slug: 'general' },
  { label: enSettings.nav.data, slug: 'data' },
  { label: enSettings.nav.public_shares, slug: 'public-shares' },
  { label: enSettings.nav.mcp_tokens, slug: 'mcp-tokens' },
  { label: enSettings.nav.webhooks, slug: 'webhooks' },
  // `/settings/ai` sends an admin on to the connection tab.
  { label: enSettings.nav.ai, slug: 'ai/connection' },
  { label: enSettings.nav.weekly_digest, slug: 'weekly-digest' },
];

interface NavGroupText {
  label: string;
  items: string[];
}

/** Reads the sub-nav as [group label, link texts] pairs (textContent, so CSS casing is irrelevant). */
async function readNavGroups(page: Page): Promise<NavGroupText[]> {
  const nav = page.getByRole('navigation', { name: enSettings.sections_label });
  await expect(nav).toBeVisible({ timeout: 15_000 });
  return nav.evaluate((el) =>
    Array.from(el.children).map((group) => ({
      label: group.querySelector('span')?.textContent?.trim() ?? '',
      items: Array.from(group.querySelectorAll('a')).map((a) => a.textContent?.trim() ?? ''),
    })),
  );
}

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

  test('admin sees every item, grouped and ordered', async ({ page }) => {
    const owner = await createTestTenant({ locale: 'en' });
    try {
      await injectAuth(page.context(), owner);
      await page.goto(`/workspaces/${owner.workspaceId}/settings/general`);

      const n = enSettings.nav;
      const expected: NavGroupText[] = [
        {
          label: enSettings.nav_group.workspace,
          items: [n.general, n.data, n.public_shares, n.ai],
        },
        { label: enSettings.nav_group.integrations, items: [n.mcp_tokens, n.webhooks] },
        { label: enSettings.nav_group.reports, items: [n.weekly_digest, n.audit_log] },
      ];

      // The audit log link is behind a build-time feature flag; every other
      // item must be present in order.
      await expect
        .poll(async () => {
          const groups = await readNavGroups(page);
          const last = groups.at(-1);
          if (last?.items.at(-1) === n.audit_log) return groups;
          return groups.map((g) => ({
            label: g.label,
            items: g === last ? [...g.items, n.audit_log] : g.items,
          }));
        })
        .toEqual(expected);
    } finally {
      await cleanupTenant(owner);
    }
  });

  test('member sees exactly the six non-admin items and a read-only general page', async ({
    page,
  }) => {
    const owner = await createTestTenant({ locale: 'en' });
    const member = await createMember(owner, 'member');
    try {
      await injectAuth(page.context(), member);
      await page.goto(`/workspaces/${owner.workspaceId}/settings/general`);

      const n = enSettings.nav;
      await expect
        .poll(async () => (await readNavGroups(page)).flatMap((g) => g.items), { timeout: 15_000 })
        .toEqual([n.general, n.data, n.public_shares, n.ai, n.mcp_tokens, n.weekly_digest]);
      const groups = await readNavGroups(page);
      expect(groups.map((g) => g.label)).toEqual([
        enSettings.nav_group.workspace,
        enSettings.nav_group.integrations,
        enSettings.nav_group.reports,
      ]);

      const sidebar = page.getByRole('navigation', { name: enSettings.sections_label });
      for (const hidden of [n.webhooks, n.audit_log]) {
        await expect(sidebar.getByRole('link', { name: hidden, exact: true })).toHaveCount(0);
      }

      const general = enSettings.workspace.general;
      await expect(page.getByText(general.read_only_note)).toBeVisible({ timeout: 10_000 });
      await expect(
        page.getByRole('textbox', { name: general.field.name, exact: true }),
      ).toBeDisabled();
      await expect(
        page.getByRole('textbox', { name: general.field.slug, exact: true }),
      ).toBeDisabled();
      await expect(
        page.getByRole('textbox', { name: general.field.description, exact: true }),
      ).toBeDisabled();
      await expect(page.getByRole('button', { name: general.save })).toHaveCount(0);
    } finally {
      await cleanupTenant(member);
      await cleanupTenant(owner);
    }
  });
});
