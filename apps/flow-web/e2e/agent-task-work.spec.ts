/**
 * Agent work on a task E2E (`/tasks/{id}`): proposals, run-now and the
 * agent comment image rule.
 *
 * Every request reaches the real flow-api. The provider, model, agent,
 * grants, task and assignment are created through REST (the provider is
 * never called: no run is started). Pending proposals and the agent-authored
 * comment have no public creation path, so those rows are written directly,
 * the same way the retro-queue spec seeds rows the API refuses to create.
 * Approve and reject then go through the real endpoints.
 *
 * Cases:
 *   A. a pending priority proposal is approved from the task page; the task
 *      really changes priority and the card goes away.
 *   B. a proposal is rejected with a reason; the row stores it and the
 *      task is untouched.
 *   C. a paused agent's Run now is disabled with the reason shown inline.
 *   D. an agent comment with a Markdown image renders a link and the page
 *      makes no request to the image host.
 */

import { randomUUID } from 'node:crypto';

import { expect, type Page, test } from '@playwright/test';
import mysql, { type Pool } from 'mysql2/promise';

import enAiAgents from '../locales/en/aiAgents.json' with { type: 'json' };
import {
  API_BASE_URL,
  cleanupTenant,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

const agentCopy = enAiAgents.task_detail.agent;
const copy = {
  proposalsTitle: agentCopy.proposals.title,
  approve: agentCopy.proposals.approve,
  reject: agentCopy.proposals.reject,
  rejectTitle: agentCopy.proposals.reject_title,
  rejectReason: agentCopy.proposals.reject_reason,
  rejectConfirm: agentCopy.proposals.reject_confirm,
  opSetPriority: agentCopy.proposals.ops.set_priority,
  runNow: agentCopy.run_now.label,
  pausedReason: agentCopy.run_now.reasons.paused,
  pausedBadge: agentCopy.run_now.paused_badge,
} as const;

const IMAGE_HOST = 'img.example.invalid';
const IMAGE_URL = `http://${IMAGE_HOST}/x.png`;

const BEFORE_PRIORITY = 2;
const AFTER_PRIORITY = 3;
const SET_PRIORITY = 'set_priority';
const ADD_COMMENT = 'add_comment';

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

interface SeededTask {
  id: string;
  title: string;
}

async function seedTask(tenant: TestTenant, title: string): Promise<SeededTask> {
  const task = await api<{ id: string; title: string }>(tenant, 'POST', '/tasks', {
    title,
    projectId: tenant.projectId,
    priority: BEFORE_PRIORITY,
  });
  return { id: task.id, title: task.title };
}

interface Fixture {
  tenant: TestTenant;
  task: SeededTask;
  agentId: string;
  agentName: string;
}

/** A tenant with a task and an agent assigned to it through the real endpoints. */
async function seedAssignedAgent(
  title: string,
  opts: { grants?: Record<string, string>; paused?: boolean } = {},
): Promise<Fixture> {
  const tenant = await createTestTenant({ locale: 'en' });
  const task = await seedTask(tenant, `${title} ${Date.now().toString(36)}`);
  const modelId = await createModel(tenant);
  const agentName = `Work ${randomUUID().slice(0, 8)}`;
  const agentId = await createAgent(tenant, modelId, agentName, opts.grants);
  await api(tenant, 'POST', `/tasks/${task.id}/agents`, { agentId, role: 'assignee' });
  if (opts.paused) {
    await api(tenant, 'POST', `/workspaces/${tenant.workspaceId}/ai/agents/${agentId}/pause`, {
      paused: true,
    });
  }
  return { tenant, task, agentId, agentName };
}

/** Internal ids of the rows a direct write needs, resolved from public ids. */
async function internalIds(
  f: Fixture,
): Promise<{ ws: number; task: number; agent: number; user: number }> {
  const [rows] = await getDb().query<mysql.RowDataPacket[]>(
    `SELECT w.id AS ws, t.id AS task, a.id AS agent, u.id AS user
       FROM workspaces w
       JOIN tasks t ON t.workspace_id = w.id AND t.public_id = UUID_TO_BIN(?, 0)
       JOIN ai_agents a ON a.workspace_id = w.id AND a.public_id = UUID_TO_BIN(?, 0)
       JOIN users u ON u.public_id = UUID_TO_BIN(?, 0)
      WHERE w.public_id = UUID_TO_BIN(?, 0)`,
    [f.task.id, f.agentId, f.tenant.userId, f.tenant.workspaceId],
  );
  const row = rows[0];
  if (!row) throw new Error('internalIds: fixture rows not found');
  return { ws: row.ws, task: row.task, agent: row.agent, user: row.user };
}

/**
 * Writes a pending set_priority proposal, shaped like the one the runtime
 * stores: normalized args plus the priority it was proposed against.
 */
async function seedProposal(f: Fixture): Promise<string> {
  const ids = await internalIds(f);
  const publicId = randomUUID();
  await getDb().query(
    `INSERT INTO agent_action_proposals
       (public_id, workspace_id, task_id, agent_id, delegated_by_user_id, run_key, step, kind,
        args_json, precondition_json, status, expires_at)
     VALUES (UUID_TO_BIN(?, 0), ?, ?, ?, ?, ?, 1, ?, CAST(? AS JSON), CAST(? AS JSON),
             'pending', UTC_TIMESTAMP(3) + INTERVAL 7 DAY)`,
    [
      publicId,
      ids.ws,
      ids.task,
      ids.agent,
      ids.user,
      randomUUID(),
      SET_PRIORITY,
      JSON.stringify({ priority: AFTER_PRIORITY }),
      JSON.stringify({ priority: BEFORE_PRIORITY }),
    ],
  );
  return publicId;
}

/** Writes a comment authored by the agent. */
async function seedAgentComment(f: Fixture, body: string): Promise<void> {
  const ids = await internalIds(f);
  await getDb().query(
    `INSERT INTO comments
       (public_id, workspace_id, task_id, author_kind, author_agent_id,
        author_agent_public_id, author_agent_name, body)
     VALUES (UUID_TO_BIN(?, 0), ?, ?, 'agent', ?, UUID_TO_BIN(?, 0), ?, ?)`,
    [randomUUID(), ids.ws, ids.task, ids.agent, f.agentId, f.agentName, body],
  );
}

async function proposalRow(publicId: string): Promise<{ status: string; reason: string | null }> {
  const [rows] = await getDb().query<mysql.RowDataPacket[]>(
    `SELECT status, decision_reason FROM agent_action_proposals WHERE public_id = UUID_TO_BIN(?, 0)`,
    [publicId],
  );
  const row = rows[0];
  if (!row) throw new Error(`proposal ${publicId} not found`);
  return { status: row.status as string, reason: row.decision_reason as string | null };
}

async function taskPriority(f: Fixture): Promise<number> {
  const task = await api<{ priority: number }>(f.tenant, 'GET', `/tasks/${f.task.id}`);
  return task.priority;
}

test.afterAll(async () => {
  await dbPool?.end();
  dbPool = null;
});

test.describe('task detail — agent work', () => {
  test.use({ viewport: { width: 1280, height: 1000 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  async function open(page: Page, f: Fixture): Promise<void> {
    tenant = f.tenant;
    await injectAuth(page.context(), f.tenant);
    await page.goto(`/tasks/${f.task.id}`);
    await expect(page.getByText(f.task.title).first()).toBeVisible({ timeout: 15_000 });
  }

  test('A: approving a pending proposal applies the change on the page', async ({ page }) => {
    const f = await seedAssignedAgent('Agent approve', { grants: { [SET_PRIORITY]: 'approval' } });
    const proposalId = await seedProposal(f);
    await open(page, f);

    const section = page.getByRole('region', { name: copy.proposalsTitle });
    await expect(section).toBeVisible({ timeout: 15_000 });
    const card = section.getByRole('listitem');
    await expect(card).toHaveCount(1);
    await expect(card.getByText(copy.opSetPriority, { exact: true })).toBeVisible();
    await expect(card.getByText(f.agentName, { exact: true })).toBeVisible();
    await expect(card).toContainText('Medium');
    await expect(card).toContainText('High');
    // The priority picker still shows the old value.
    await expect(page.getByRole('button', { name: 'Medium', exact: true })).toBeVisible();

    await card.getByRole('button', { name: copy.approve, exact: true }).click();

    await expect(section).toHaveCount(0, { timeout: 15_000 });
    await expect(page.getByRole('button', { name: 'High', exact: true })).toBeVisible({
      timeout: 15_000,
    });
    expect(await taskPriority(f)).toBe(AFTER_PRIORITY);
    expect((await proposalRow(proposalId)).status).toBe('applied');
  });

  test('B: rejecting with a reason stores it and leaves the task alone', async ({ page }) => {
    const f = await seedAssignedAgent('Agent reject', { grants: { [SET_PRIORITY]: 'approval' } });
    const proposalId = await seedProposal(f);
    await open(page, f);

    const section = page.getByRole('region', { name: copy.proposalsTitle });
    await expect(section).toBeVisible({ timeout: 15_000 });
    await section.getByRole('button', { name: copy.reject, exact: true }).click();

    const dialog = page.getByRole('dialog', { name: copy.rejectTitle });
    await expect(dialog).toBeVisible();
    await dialog.getByLabel(copy.rejectReason).fill('Too early to change this.');
    await dialog.getByRole('button', { name: copy.rejectConfirm, exact: true }).click();

    await expect(dialog).toBeHidden();
    await expect(section).toHaveCount(0, { timeout: 15_000 });
    expect(await proposalRow(proposalId)).toEqual({
      status: 'rejected',
      reason: 'Too early to change this.',
    });
    expect(await taskPriority(f)).toBe(BEFORE_PRIORITY);
    await expect(page.getByRole('button', { name: 'Medium', exact: true })).toBeVisible();
  });

  test('C: Run now is disabled for a paused agent and says why', async ({ page }) => {
    const f = await seedAssignedAgent('Agent paused', {
      grants: { [SET_PRIORITY]: 'approval' },
      paused: true,
    });
    await open(page, f);

    const runNow = page.getByRole('button', { name: copy.runNow, exact: true });
    await expect(runNow).toBeVisible({ timeout: 15_000 });
    await expect(runNow).toBeDisabled();
    await expect(page.getByText(copy.pausedReason, { exact: true })).toBeVisible();
    await expect(runNow).toHaveAccessibleDescription(copy.pausedReason);
    await expect(page.getByText(copy.pausedBadge, { exact: true })).toBeVisible();
  });

  test('D: an agent comment image becomes a link and is never requested', async ({ page }) => {
    const f = await seedAssignedAgent('Agent comment', { grants: { [ADD_COMMENT]: 'auto' } });
    await seedAgentComment(f, `Here is the chart ![a](${IMAGE_URL})`);

    // Observe the external host only; a request that does reach it fails fast.
    const imageRequests: string[] = [];
    page.on('request', (req) => {
      if (new URL(req.url()).hostname === IMAGE_HOST) imageRequests.push(req.url());
    });
    await page.route(`**://${IMAGE_HOST}/**`, (route) => route.abort());
    await open(page, f);

    const link = page.getByRole('link', { name: 'a', exact: true });
    await expect(link).toBeVisible({ timeout: 15_000 });
    await expect(link).toHaveAttribute('href', IMAGE_URL);
    await expect(page.getByText(f.agentName, { exact: true }).first()).toBeVisible();
    await expect(page.locator(`img[src*="${IMAGE_HOST}"]`)).toHaveCount(0);
    expect(imageRequests).toEqual([]);
  });
});
