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
import {
  AUTH_API_URL,
  cleanupTenant,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

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

/** Invites a fresh user into `owner`'s workspace as a member and accepts on their behalf. */
async function createMember(owner: TestTenant): Promise<TestTenant> {
  const createRes = await fetch(`${AUTH_API_URL}/workspaces/${owner.workspaceId}/invites`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${owner.accessToken}`,
    },
    body: JSON.stringify({ role: 'member' }),
  });
  if (!createRes.ok) {
    throw new Error(`create invite failed: ${createRes.status} ${await createRes.text()}`);
  }
  const { token } = (await createRes.json()) as { token: string };

  const member = await createTestTenant({ locale: 'en' });
  const acceptRes = await fetch(`${AUTH_API_URL}/invites/${token}/accept`, {
    method: 'POST',
    headers: { accept: 'application/json', authorization: `Bearer ${member.accessToken}` },
  });
  if (!acceptRes.ok) {
    throw new Error(`accept invite failed: ${acceptRes.status} ${await acceptRes.text()}`);
  }
  return member;
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
          items: [n.general, n.data, n.public_shares],
        },
        { label: enSettings.nav_group.integrations, items: [n.mcp_tokens, n.webhooks] },
        {
          label: enSettings.nav_group.ai,
          items: [n.ai_providers, n.ai_agents, n.auto_actions],
        },
        {
          label: enSettings.nav_group.reports,
          items: [n.ai_activity, n.ai_metrics, n.weekly_digest, n.audit_log],
        },
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

  test('member sees exactly the eight non-admin items and a read-only general page', async ({
    page,
  }) => {
    const owner = await createTestTenant({ locale: 'en' });
    const member = await createMember(owner);
    try {
      await injectAuth(page.context(), member);
      await page.goto(`/workspaces/${owner.workspaceId}/settings/general`);

      const n = enSettings.nav;
      await expect
        .poll(async () => (await readNavGroups(page)).flatMap((g) => g.items), { timeout: 15_000 })
        .toEqual([
          n.general,
          n.data,
          n.public_shares,
          n.mcp_tokens,
          n.ai_agents,
          n.ai_activity,
          n.ai_metrics,
          n.weekly_digest,
        ]);
      const groups = await readNavGroups(page);
      expect(groups.map((g) => g.label)).toEqual([
        enSettings.nav_group.workspace,
        enSettings.nav_group.integrations,
        enSettings.nav_group.ai,
        enSettings.nav_group.reports,
      ]);

      const sidebar = page.getByRole('navigation', { name: enSettings.sections_label });
      for (const hidden of [n.webhooks, n.ai_providers, n.auto_actions, n.audit_log]) {
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
