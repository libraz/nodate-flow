/**
 * AI settings page E2E (`/workspaces/{wsId}/settings/ai/*`).
 *
 * One page with four route-linked tabs (connection, automation, agents,
 * usage) under a status card. Admins see all four tabs; members and guests
 * see agents and usage only.
 *
 * Cases:
 *   A. admin: every tab shows the landmarks of its content.
 *   B. admin: the status card of a workspace without a provider points at the
 *      connection tab through "Set up".
 *   C. admin: `/settings/ai` redirects to the connection tab.
 *   D. member: only the agents and usage tabs, `/settings/ai` redirects to
 *      agents, and the agents tab carries no link to an admin-only page.
 *   E. member: opening the connection tab by URL shows the tab-sized
 *      "restricted" panel instead of the provider list.
 *   F. guest: same two tabs as a member.
 *
 * Each test creates its own tenants through REST so the suite stays
 * parallel-safe.
 */

import { expect, type Page, test } from '@playwright/test';

import enAi from '../locales/en/ai.json' with { type: 'json' };
import enAiSuggestions from '../locales/en/ai-suggestions.json' with { type: 'json' };
import enAiMetrics from '../locales/en/aiMetrics.json' with { type: 'json' };
import enSettings from '../locales/en/settings.json' with { type: 'json' };
import {
  cleanupTenant,
  createMember,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

const aiPage = enSettings.ai_page;
const status = aiPage.status;

const copy = {
  pageTitle: aiPage.title,
  tabsLabel: aiPage.tabs_label,
  tabConnection: aiPage.tabs.connection,
  tabAutomation: aiPage.tabs.automation,
  tabAgents: aiPage.tabs.agents,
  tabUsage: aiPage.tabs.usage,
  agentsNote: aiPage.agents_note,
  statusTitle: status.title,
  featuresLabel: status.features.label,
  featuresNone: status.features.none,
  featuresAskAdmin: status.ask_admin,
  embeddingLabel: status.embedding.label,
  autoActionsLabel: status.auto_actions.label,
  autoActionsOn: status.auto_actions.on.replace('{minutes}', '5'),
  fixLink: status.fix_link,
  providersTitle: enAi.providers.title,
  embeddingTitle: enAi.providers.embedding.title,
  matrixTitle: enAi.autonomy.matrix.title,
  rulesTitle: enSettings.workspace.auto_actions.rules.title,
  agentsTitle: enAiSuggestions.agents.title,
  agentsEmptyMember: enAiSuggestions.agents.empty_member,
  usageActivityTitle: enSettings.ai_activity.title,
  metricsTitle: enAiMetrics.page.title,
  metricsProposed: enAiMetrics.kpi.proposed,
  restrictedBody: enSettings.access_restricted.body,
} as const;

const TAB_TIMEOUT = 15_000;

function settingsUrl(tenant: TestTenant, path: string): string {
  return `/workspaces/${tenant.workspaceId}/settings/${path}`;
}

function tabNav(page: Page) {
  return page.getByRole('navigation', { name: copy.tabsLabel });
}

/** Link texts of the tab navigation, in DOM order. */
async function tabLabels(page: Page): Promise<string[]> {
  const nav = tabNav(page);
  await expect(nav).toBeVisible({ timeout: TAB_TIMEOUT });
  return nav.getByRole('link').allTextContents();
}

test.describe('ai settings page — admin', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  let owner: TestTenant | null = null;

  test.afterEach(async () => {
    if (owner) {
      await cleanupTenant(owner);
      owner = null;
    }
  });

  test('A: each tab renders the landmarks of its content', async ({ page }) => {
    owner = await createTestTenant({ locale: 'en' });
    await injectAuth(page.context(), owner);
    const main = page.getByRole('main');

    await page.goto(settingsUrl(owner, 'ai/connection'));
    await expect(main.getByRole('heading', { level: 1, name: copy.pageTitle })).toBeVisible({
      timeout: TAB_TIMEOUT,
    });
    expect((await tabLabels(page)).map((l) => l.trim())).toEqual([
      copy.tabConnection,
      copy.tabAutomation,
      copy.tabAgents,
      copy.tabUsage,
    ]);
    await expect(tabNav(page).getByRole('link', { name: copy.tabConnection })).toHaveAttribute(
      'aria-current',
      'page',
    );
    await expect(main.getByRole('heading', { level: 2, name: copy.providersTitle })).toBeVisible({
      timeout: TAB_TIMEOUT,
    });
    await expect(main.getByRole('heading', { name: copy.embeddingTitle })).toBeVisible();

    await tabNav(page).getByRole('link', { name: copy.tabAutomation }).click();
    await expect(page).toHaveURL(/\/settings\/ai\/automation$/);
    await expect(main.getByRole('table', { name: copy.matrixTitle })).toBeVisible({
      timeout: TAB_TIMEOUT,
    });
    await expect(
      main.getByRole('form', { name: copy.rulesTitle }).getByRole('table'),
    ).toBeVisible();

    await tabNav(page).getByRole('link', { name: copy.tabAgents }).click();
    await expect(page).toHaveURL(/\/settings\/ai\/agents$/);
    await expect(main.getByRole('heading', { level: 2, name: copy.agentsTitle })).toBeVisible({
      timeout: TAB_TIMEOUT,
    });

    await tabNav(page).getByRole('link', { name: copy.tabUsage }).click();
    await expect(page).toHaveURL(/\/settings\/ai\/usage/);
    await expect(
      main.getByRole('heading', { level: 2, name: copy.usageActivityTitle }),
    ).toBeVisible({ timeout: TAB_TIMEOUT });
    await expect(main.getByRole('heading', { level: 2, name: copy.metricsTitle })).toBeVisible();
    await expect(main.getByText(copy.metricsProposed)).toBeVisible();

    // The page keeps a single h1 across tabs.
    await expect(main.getByRole('heading', { level: 1 })).toHaveCount(1);
  });

  test('B: status card of a workspace without a provider leads to the connection tab', async ({
    page,
  }) => {
    owner = await createTestTenant({ locale: 'en' });
    await injectAuth(page.context(), owner);
    await page.goto(settingsUrl(owner, 'ai/agents'));

    const card = page.getByRole('region', { name: copy.statusTitle });
    await expect(card).toBeVisible({ timeout: TAB_TIMEOUT });
    const rows = card.getByRole('listitem');
    await expect(rows).toHaveCount(3);

    const features = rows.filter({ hasText: copy.featuresLabel });
    await expect(features).toContainText(copy.featuresNone);
    await expect(rows.filter({ hasText: copy.embeddingLabel })).toHaveCount(1);
    const autoActions = rows.filter({ hasText: copy.autoActionsLabel });
    await expect(autoActions).toContainText(copy.autoActionsOn);

    // The note under the card says agents are optional.
    await expect(page.getByText(copy.agentsNote)).toBeVisible();

    await features.getByRole('link', { name: copy.fixLink }).click();
    await expect(page).toHaveURL(/\/settings\/ai\/connection$/);
    await expect(
      page.getByRole('main').getByRole('heading', { level: 2, name: copy.providersTitle }),
    ).toBeVisible({ timeout: TAB_TIMEOUT });
  });

  test('C: /settings/ai redirects an admin to the connection tab', async ({ page }) => {
    owner = await createTestTenant({ locale: 'en' });
    await injectAuth(page.context(), owner);
    await page.goto(settingsUrl(owner, 'ai'));

    await expect(page).toHaveURL(/\/settings\/ai\/connection$/, { timeout: TAB_TIMEOUT });
    await expect(
      page.getByRole('main').getByRole('heading', { level: 2, name: copy.providersTitle }),
    ).toBeVisible({ timeout: TAB_TIMEOUT });
  });
});

test.describe('ai settings page — member and guest', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  let owner: TestTenant | null = null;
  let invitee: TestTenant | null = null;

  test.afterEach(async () => {
    if (invitee) await cleanupTenant(invitee);
    if (owner) await cleanupTenant(owner);
    invitee = null;
    owner = null;
  });

  test('D: member sees agents and usage only, with no link to admin pages', async ({ page }) => {
    owner = await createTestTenant({ locale: 'en' });
    invitee = await createMember(owner, 'member');
    await injectAuth(page.context(), invitee);

    await page.goto(settingsUrl(owner, 'ai'));
    await expect(page).toHaveURL(/\/settings\/ai\/agents$/, { timeout: TAB_TIMEOUT });
    expect((await tabLabels(page)).map((l) => l.trim())).toEqual([copy.tabAgents, copy.tabUsage]);

    const main = page.getByRole('main');
    await expect(main.getByRole('heading', { level: 2, name: copy.agentsTitle })).toBeVisible({
      timeout: TAB_TIMEOUT,
    });
    // No provider and so no model: the empty state points at the admin, not at a page.
    await expect(main.getByText(copy.agentsEmptyMember)).toBeVisible();

    const card = page.getByRole('region', { name: copy.statusTitle });
    await expect(card.getByRole('listitem').filter({ hasText: copy.featuresLabel })).toContainText(
      copy.featuresAskAdmin,
    );
    await expect(card.getByRole('link')).toHaveCount(0);

    const adminLinks = main.locator(
      [
        'a[href$="/settings/ai/connection"]',
        'a[href$="/settings/ai/automation"]',
        'a[href$="/settings/webhooks"]',
        'a[href$="/settings/audit-log"]',
      ].join(', '),
    );
    await expect(adminLinks).toHaveCount(0);

    await tabNav(page).getByRole('link', { name: copy.tabUsage }).click();
    await expect(page).toHaveURL(/\/settings\/ai\/usage/);
    await expect(main.getByRole('heading', { level: 2, name: copy.metricsTitle })).toBeVisible({
      timeout: TAB_TIMEOUT,
    });
  });

  test('E: member opening the connection tab by URL gets the restricted panel', async ({
    page,
  }) => {
    owner = await createTestTenant({ locale: 'en' });
    invitee = await createMember(owner, 'member');
    await injectAuth(page.context(), invitee);

    await page.goto(settingsUrl(owner, 'ai/connection'));
    const main = page.getByRole('main');
    await expect(main.getByRole('heading', { level: 2, name: copy.pageTitle })).toBeVisible({
      timeout: TAB_TIMEOUT,
    });
    await expect(main.getByText(copy.restrictedBody)).toBeVisible();
    await expect(main.getByRole('heading', { name: copy.providersTitle })).toHaveCount(0);
  });

  test('F: guest sees the same two tabs as a member', async ({ page }) => {
    owner = await createTestTenant({ locale: 'en' });
    invitee = await createMember(owner, 'guest');
    await injectAuth(page.context(), invitee);

    await page.goto(settingsUrl(owner, 'ai'));
    await expect(page).toHaveURL(/\/settings\/ai\/agents$/, { timeout: TAB_TIMEOUT });
    expect((await tabLabels(page)).map((l) => l.trim())).toEqual([copy.tabAgents, copy.tabUsage]);
    await expect(
      page.getByRole('main').getByRole('heading', { level: 2, name: copy.agentsTitle }),
    ).toBeVisible({ timeout: TAB_TIMEOUT });
  });
});
