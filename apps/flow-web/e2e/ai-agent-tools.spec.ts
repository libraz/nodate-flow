/**
 * Agent grants editor E2E (`/workspaces/{wsId}/settings/ai/agents`).
 *
 * Everything reaches the real flow-api: the provider, model and agents are
 * created through REST (the provider is never called), grants are stored by
 * the real PATCH .../tools, and the page reads them back from the real list.
 * The one direct write is turning an agent into a signal judge, which has no
 * public creation path.
 *
 * Cases:
 *   A. admin: changing a grant and saving survives a reload and the API
 *      reports the same grants.
 *   B. admin: an agent with `{}` shows the nothing-allowed warning.
 *   C. member: the controls are disabled and there is no save button.
 *   D. a signal judge has no grants editor.
 */

import { randomUUID } from 'node:crypto';

import { expect, type Page, test } from '@playwright/test';
import mysql, { type Pool } from 'mysql2/promise';

import enAiSuggestions from '../locales/en/ai-suggestions.json' with { type: 'json' };
import {
  API_BASE_URL,
  cleanupTenant,
  createMember,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

const tools = enAiSuggestions.agents.tools;

const copy = {
  title: tools.title,
  emptyWarning: tools.emptyWarning,
  inactive: tools.inactive,
  save: tools.save,
  readOnly: tools.readOnly,
  off: tools.modes.off,
  approval: tools.modes.approval,
  auto: tools.modes.auto,
  addComment: tools.operations.add_comment.label,
  setPriority: tools.operations.set_priority.label,
} as const;

const OPERATION_NAMES = [
  'add_comment',
  'set_priority',
  'set_due_on',
  'transition_task',
  'create_subtask',
] as const;

/** Wire names of the operations, used as computed keys to keep snake_case out of literals. */
const OP = { addComment: 'add_comment', setPriority: 'set_priority' } as const;

const ADMIN_FETCH_HEADERS = { 'Content-Type': 'application/json', accept: 'application/json' };

async function api<T>(
  tenant: TestTenant,
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const res = await fetch(`${API_BASE_URL}${path}`, {
    method,
    headers: { ...ADMIN_FETCH_HEADERS, authorization: `Bearer ${tenant.accessToken}` },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const text = await res.text();
  if (!res.ok) {
    throw new Error(`${method} ${path} -> ${res.status} ${text}`);
  }
  return (text ? JSON.parse(text) : {}) as T;
}

/**
 * Registers a provider (never called: no run is started) and returns the
 * public id of its default model.
 */
async function createModel(tenant: TestTenant): Promise<string> {
  const ws = `/workspaces/${tenant.workspaceId}/ai`;
  const provider = await api<{ id: string }>(tenant, 'POST', `${ws}/providers`, {
    kind: 'openai_compat',
    name: `E2E ${randomUUID().slice(0, 8)}`,
    baseUrl: 'https://api.openai.com/v1',
    defaultModel: 'e2e-model',
    apiKey: 'sk-e2e-fixture-0123456789',
  });
  const models = await api<{ models: { id: string; isDefault: boolean }[] }>(
    tenant,
    'GET',
    `${ws}/providers/${provider.id}/models`,
  );
  const model = models.models.find((m) => m.isDefault) ?? models.models[0];
  if (!model) throw new Error('provider has no model');
  return model.id;
}

/** Creates a task agent through the API; grants are set through PATCH .../tools. */
async function createAgent(
  tenant: TestTenant,
  modelId: string,
  name: string,
  grants?: Record<string, string>,
): Promise<string> {
  const ws = `/workspaces/${tenant.workspaceId}/ai/agents`;
  const created = await api<{ id: string }>(tenant, 'POST', ws, {
    modelId,
    name,
    systemPrompt: 'Work the assigned task.',
    scheduleKind: 'disabled',
  });
  if (grants) {
    await api(tenant, 'PATCH', `${ws}/${created.id}/tools`, { tools: grants });
  }
  return created.id;
}

let dbPool: Pool | null = null;
/** Lazily created pool; the dev stack exposes MySQL on 127.0.0.1:3306 (NF_DB_* overrides). */
function getDb(): Pool {
  if (dbPool) return dbPool;
  dbPool = mysql.createPool({
    host: process.env.NF_DB_HOST ?? '127.0.0.1',
    port: Number(process.env.NF_DB_PORT ?? 3306),
    user: process.env.NF_DB_USER ?? 'nodate',
    password: process.env.NF_DB_PASSWORD ?? 'nodatepw',
    database: process.env.NF_DB_NAME ?? 'nodate_flow',
    connectionLimit: 4,
    charset: 'utf8mb4_0900_ai_ci',
  });
  return dbPool;
}

function agentsUrl(tenant: TestTenant): string {
  return `/workspaces/${tenant.workspaceId}/settings/ai/agents`;
}

/** The grants section of the (single) task agent, expanded. */
async function openGrants(page: Page) {
  const region = page.getByRole('region', { name: copy.title });
  await expect(region).toBeVisible({ timeout: 15_000 });
  const toggle = region.getByRole('button', { name: new RegExp(copy.title) });
  if ((await toggle.getAttribute('aria-expanded')) !== 'true') {
    await toggle.click();
  }
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  return region;
}

function modeRadio(region: ReturnType<Page['getByRole']>, operation: string, mode: string) {
  return region.getByRole('radiogroup', { name: operation }).getByRole('radio', { name: mode });
}

test.afterAll(async () => {
  await dbPool?.end();
  dbPool = null;
});

test.describe('ai agents — grants editor', () => {
  test.use({ viewport: { width: 1280, height: 1000 } });

  const tenants: TestTenant[] = [];

  test.afterEach(async () => {
    while (tenants.length > 0) {
      const t = tenants.pop();
      if (t) await cleanupTenant(t);
    }
  });

  /** A fresh workspace owner with one task agent holding `grants`. */
  async function seedAgent(
    grants: Record<string, string>,
  ): Promise<{ owner: TestTenant; agentId: string }> {
    const owner = await createTestTenant({ locale: 'en' });
    tenants.push(owner);
    const modelId = await createModel(owner);
    const agentId = await createAgent(owner, modelId, `Grants ${randomUUID().slice(0, 8)}`, grants);
    return { owner, agentId };
  }

  async function storedGrants(owner: TestTenant, agentId: string): Promise<unknown> {
    const list = await api<{ agents: { id: string; tools: unknown }[] }>(
      owner,
      'GET',
      `/workspaces/${owner.workspaceId}/ai/agents`,
    );
    return list.agents.find((a) => a.id === agentId)?.tools;
  }

  test('A: an admin changes a grant, saves, and it persists after reload', async ({ page }) => {
    const { owner, agentId } = await seedAgent({ [OP.addComment]: 'auto' });
    await injectAuth(page.context(), owner);
    await page.goto(agentsUrl(owner));

    const region = await openGrants(page);
    await expect(modeRadio(region, copy.addComment, copy.auto)).toHaveAttribute(
      'aria-checked',
      'true',
    );
    await expect(modeRadio(region, copy.setPriority, copy.off)).toHaveAttribute(
      'aria-checked',
      'true',
    );

    await modeRadio(region, copy.setPriority, copy.approval).click();
    const saved = page.waitForResponse(
      (res) => res.request().method() === 'PATCH' && res.url().endsWith(`/${agentId}/tools`),
    );
    await region.getByRole('button', { name: copy.save, exact: true }).click();
    expect((await saved).status()).toBe(200);

    expect(await storedGrants(owner, agentId)).toEqual({
      [OP.addComment]: 'auto',
      [OP.setPriority]: 'approval',
    });

    await page.reload();
    const reloaded = await openGrants(page);
    await expect(modeRadio(reloaded, copy.setPriority, copy.approval)).toHaveAttribute(
      'aria-checked',
      'true',
    );
    await expect(modeRadio(reloaded, copy.addComment, copy.auto)).toHaveAttribute(
      'aria-checked',
      'true',
    );
    await expect(reloaded.getByText(copy.emptyWarning)).toHaveCount(0);
  });

  test('B: an agent with `{}` shows the nothing-allowed warning', async ({ page }) => {
    const { owner } = await seedAgent({});
    await injectAuth(page.context(), owner);
    await page.goto(agentsUrl(owner));

    const region = await openGrants(page);
    await expect(region.getByText(copy.inactive, { exact: true })).toBeVisible();
    await expect(region.getByRole('note')).toHaveText(copy.emptyWarning);
    for (const op of OPERATION_NAMES) {
      const label = tools.operations[op].label;
      await expect(modeRadio(region, label, copy.off)).toHaveAttribute('aria-checked', 'true');
    }
  });

  test('C: a member sees the grants with disabled controls and no save', async ({ page }) => {
    const { owner } = await seedAgent({ [OP.setPriority]: 'approval' });
    const member = await createMember(owner, 'member');
    tenants.push(member);
    await injectAuth(page.context(), member);
    await page.goto(agentsUrl(owner));

    const region = await openGrants(page);
    await expect(modeRadio(region, copy.setPriority, copy.approval)).toHaveAttribute(
      'aria-checked',
      'true',
    );
    for (const op of OPERATION_NAMES) {
      const group = region.getByRole('radiogroup', { name: tools.operations[op].label });
      await expect(group).toHaveAttribute('aria-disabled', 'true');
      for (const radio of await group.getByRole('radio').all()) {
        await expect(radio).toBeDisabled();
      }
    }
    await expect(region.getByText(copy.readOnly)).toBeVisible();
    await expect(region.getByRole('button', { name: copy.save, exact: true })).toHaveCount(0);
  });

  test('D: a signal judge has no grants editor', async ({ page }) => {
    const { owner } = await seedAgent({ [OP.addComment]: 'auto' });
    const modelId = await createModel(owner);
    const judgeName = `Judge ${randomUUID().slice(0, 8)}`;
    const judgeId = await createAgent(owner, modelId, judgeName);
    // A judge has no public creation path: flip the row's kind. SQL NULL is
    // the grants value a judge really stores.
    await getDb().query(
      `UPDATE ai_agents SET kind = 'signal_judge', tools_json = NULL
         WHERE public_id = UUID_TO_BIN(?, 0)`,
      [judgeId],
    );
    await injectAuth(page.context(), owner);
    await page.goto(agentsUrl(owner));

    await expect(page.getByText(judgeName, { exact: true })).toBeVisible({ timeout: 15_000 });
    // Two agents on the page, one grants section: the judge carries none.
    await expect(page.getByRole('region', { name: copy.title })).toHaveCount(1);
  });
});
