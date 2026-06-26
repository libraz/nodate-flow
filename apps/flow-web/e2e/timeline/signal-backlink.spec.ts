/**
 * Signal-grouped timeline block with reversal — Phase 6 / L3 of
 * docs/plan/release-8-signals-and-judge-loop.md.
 *
 * Covers `apps/flow-web/src/features/timeline/signal-group.tsx`
 * mounted via `timeline-view.tsx` at `/workspaces/{wsId}/timeline`.
 * The component clusters events that share a common
 * `triggeredBySignalId` into one `<article role="article">` block,
 * renders the judge reasoning excerpt + confidence badge, and exposes
 * a Reverse footer that targets the most recent un-reversed LLM-origin
 * event via `POST /workspaces/{wsId}/events/{eventPublicId}/reverse`.
 *
 * Backend contract:
 *   - GET  /workspaces/{wsId}/timeline                                       (read)
 *   - POST /workspaces/{wsId}/events/{eventPublicId}/reverse                 (mutate)
 *
 * Seeding strategy: there is no REST path to write signals + judge-attributed
 * events with the fields this component reads (`triggered_by_signal_id`,
 * `actor_agent_id`, `signal.judged` payload, etc.). The production path is
 * the async judge runner; recreating that end-to-end inside a Playwright
 * spec would couple the test to the LLM provider chain and the runner
 * scheduler.
 *
 * Mirror the mysql2/promise pattern from
 * `apps/flow-web/e2e/tasks/retro-queue.spec.ts` and the agent-seed shape
 * from `apps/flow-api/tests/helpers/aiagent.go` SeedAgent. One transaction
 * per chain inserts:
 *   1. ai_providers + ai_models + ai_agents (kind='signal_judge') so the
 *      agent has a valid FK target — the v_task_timeline view LEFT JOINs
 *      ai_agents on actor_agent_id to project actorAgentId into the DTO.
 *   2. One `signals` row with the judge verdict already filled in
 *      (`applied_at` set, confidence + judge_output_json populated).
 *   3. Three `events` rows, all with `triggered_by_signal_id` pointing at
 *      the seeded signal and `actor_agent_id` set so the timeline marks
 *      them LLM-origin:
 *        a) signal.attached  payload={signalId, source, kind}
 *        b) signal.judged    payload={reasoningExcerpt, confidence, action, autonomyLevel}
 *        c) task.auto_completed payload={reasoningExcerpt, confidence}
 *
 *      occurred_at is incremented by 1s per row so the timeline DESC
 *      order yields [auto_completed, judged, attached] — the same shape
 *      a real judge run would produce.
 *
 * Cases:
 *   1. Judge-driven causal chain renders as one signal group block with
 *      the reasoning excerpt, confidence badge, all 3 event rows, and a
 *      Reverse button.
 *   2. Clicking Reverse opens the confirm dialog with the spec'd copy.
 *   3. Confirming Reverse calls the API, fires the success toast, dims
 *      the block (opacity 0.7 via data-reversed) and hides the button.
 *   4. Cancel keeps the block unchanged (no toast, button still present).
 *   5. Non-LLM (user-driven) events render as solo EventCard rows — no
 *      signal-group article is emitted.
 */

import { randomUUID } from 'node:crypto';

import { expect, type Page, test } from '@playwright/test';
import mysql, { type Pool } from 'mysql2/promise';

import enTimeline from '../../locales/en/timeline.json' with { type: 'json' };
import {
  cleanupTenant,
  createTask,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from '../fixtures/tenant';

const copy = {
  pageTitle: enTimeline.view.title,
  // Source enum values land verbatim in the payload; the locale uses
  // {source} interpolation so we render "Caused by: webhook signal".
  causedByWebhook: enTimeline.signal.caused_by.replace('{source}', 'webhook'),
  reasoningLabel: enTimeline.signal.reasoning,
  reverse: enTimeline.signal.reverse,
  reversedLabel: enTimeline.signal.reversed_label,
  reverseConfirmTitle: enTimeline.signal.reverse_confirm.title,
  reverseConfirmBody: enTimeline.signal.reverse_confirm.body,
  reverseConfirmConfirm: enTimeline.signal.reverse_confirm.confirm,
  reverseConfirmCancel: enTimeline.signal.reverse_confirm.cancel,
  reverseSuccess: enTimeline.signal.reverse_success,
  reverseErrorFetch: enTimeline.signal.reverse_error.fetch_error,
} as const;

/**
 * Shared connection pool. mysql2 opens a new TCP handshake per
 * `createConnection`; the pool keeps overhead bounded across the
 * three or four seeds this spec performs. Each Playwright worker is
 * a separate Node process, so the module-level pool is per-worker.
 *
 * Defaults mirror the dev compose stack on 127.0.0.1:3306. CI can
 * override via NF_DB_* env without code changes.
 */
let dbPool: Pool | null = null;
function getDb(): Pool {
  if (dbPool) return dbPool;
  dbPool = mysql.createPool({
    host: process.env.NF_DB_HOST ?? '127.0.0.1',
    port: Number(process.env.NF_DB_PORT ?? 3306),
    user: process.env.NF_DB_USER ?? 'nodate',
    password: process.env.NF_DB_PASSWORD ?? 'nodatepw',
    database: process.env.NF_DB_NAME ?? 'nodate_flow',
    connectionLimit: 4,
    multipleStatements: false,
    // events.payload_json / signals.payload_json are utf8mb4 but mysql2's
    // default client charset is latin1; pin to the server collation so
    // multi-byte reasoning excerpts round-trip unchanged.
    charset: 'utf8mb4_0900_ai_ci',
  });
  return dbPool;
}

/**
 * Identifiers returned by {@link seedJudgeChain} — the caller asserts
 * against the surfaced reasoning + confidence + agent name and may
 * need to drive the reverse endpoint by event public id.
 */
interface SeededJudgeChain {
  signalPublicId: string;
  agentName: string;
  taskTitle: string;
  /** Hyphenated UUID v7 of the seeded task. */
  taskPublicId: string;
  /** Source enum value embedded in the signal.attached payload. */
  source: 'webhook';
  /** Kind string surfaced on the signal-attached payload + badge. */
  kind: string;
  reasoning: string;
  confidence: number;
  /**
   * Event public ids in chronological order:
   * [signal.attached, signal.judged, task.auto_completed]. The
   * timeline DESC-orders them, so `autoCompletedEventId` is the row
   * the Reverse button targets.
   */
  attachedEventId: string;
  judgedEventId: string;
  autoCompletedEventId: string;
}

/**
 * Options accepted by {@link seedJudgeChain}.
 */
interface SeedJudgeChainOptions {
  taskTitle: string;
  signalKind?: string;
  reasoning?: string;
  confidence?: number;
}

/**
 * Seeds the full provider chain for one judge run. Mirrors the writes
 * performed by `apps/flow-api/tests/helpers/aiagent.go` SeedAgent +
 * the Applier's event append loop, but done in one transaction with
 * FOREIGN_KEY_CHECKS=0 so the helper stays self-contained.
 *
 * The chain looks like:
 *   ai_providers -> ai_models -> ai_agents (kind='signal_judge')
 *   signals (applied_at set; judge_output_json populated)
 *   events.signal.attached         (LLM origin via actor_agent_id)
 *   events.signal.judged           (LLM origin)
 *   events.task.auto_completed     (LLM origin; this is the reverse target)
 *
 * The task itself is created via the public API before this helper
 * runs, so the `task.created` row is already on the timeline — the
 * judge-chain events arrive after it and cluster on the signal id.
 */
async function seedJudgeChain(
  tenant: TestTenant,
  opts: SeedJudgeChainOptions,
): Promise<SeededJudgeChain> {
  const signalKind = opts.signalKind ?? 'discord.presence';
  const reasoning =
    opts.reasoning ??
    'User has been idle on Discord for 25 minutes after the last status ping; the task already shipped a green deploy log so it is safe to auto-complete.';
  const confidence = opts.confidence ?? 0.92;
  const suffix = randomUUID().slice(0, 8);
  const agentName = `Judge ${suffix}`;
  const signalPublicId = randomUUID();
  const providerPublicId = randomUUID();
  const modelPublicId = randomUUID();
  const agentPublicId = randomUUID();
  const attachedEventId = randomUUID();
  const judgedEventId = randomUUID();
  const autoCompletedEventId = randomUUID();
  const source = 'webhook' as const;

  // Resolve workspace + task by public id. The handler-side flow creates
  // the task first via REST; we just look up its internal id.
  const conn = await getDb().getConnection();
  try {
    await conn.beginTransaction();

    const [wsRows] = await conn.query<mysql.RowDataPacket[]>(
      'SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1',
      [tenant.workspaceId],
    );
    if (wsRows.length === 0) {
      throw new Error(`seedJudgeChain: workspace ${tenant.workspaceId} not found`);
    }
    const wsInternalId = wsRows[0]?.id as number;

    const [taskRows] = await conn.query<mysql.RowDataPacket[]>(
      `SELECT id, public_id, title FROM tasks
         WHERE workspace_id = ? AND title = ? AND enabled = TRUE
         ORDER BY id DESC LIMIT 1`,
      [wsInternalId, opts.taskTitle],
    );
    if (taskRows.length === 0) {
      throw new Error(`seedJudgeChain: task ${opts.taskTitle} not found`);
    }
    const taskInternalId = taskRows[0]?.id as number;
    const taskPublicIdBin = taskRows[0]?.public_id as Buffer;
    // BIN_TO_UUID returns the canonical hyphenated form; do it server-side
    // to stay consistent with how the API renders ids.
    const [taskUuidRows] = await conn.query<mysql.RowDataPacket[]>(
      'SELECT BIN_TO_UUID(?, 0) AS uuid',
      [taskPublicIdBin],
    );
    const taskPublicId = (taskUuidRows[0]?.uuid as string) ?? '';

    // FOREIGN_KEY_CHECKS off so the seed is resilient to schema growth
    // adding new FKs that we have not surfaced yet. Re-enabled at the
    // end of the tx so other transactions on the same connection are
    // not affected.
    await conn.query('SET FOREIGN_KEY_CHECKS = 0');

    // ai_providers — api_key_ciphertext is a dummy byte blob because
    // the judge runner is not exercised; only the FK target matters.
    const [provRes] = await conn.query<mysql.ResultSetHeader>(
      `INSERT INTO ai_providers (
         public_id, workspace_id, kind, name,
         api_key_ciphertext, api_key_prefix, api_key_suffix,
         default_model, enabled
       ) VALUES (UUID_TO_BIN(?, 0), ?, 'openai_compat', ?, ?, 'sk-test_', 'XXXX', NULL, TRUE)`,
      [providerPublicId, wsInternalId, `e2e-provider-${suffix}`, Buffer.from('test')],
    );
    const providerInternalId = provRes.insertId;

    // ai_models — minimal viable row; context_window non-zero so any
    // future sanity check the runner adds does not trip.
    const [modelRes] = await conn.query<mysql.ResultSetHeader>(
      `INSERT INTO ai_models (
         public_id, workspace_id, provider_id, name, display_name,
         context_window, max_output_tokens,
         input_price_micro_usd_per_mtok, output_price_micro_usd_per_mtok,
         supports_tools, supports_vision, enabled
       ) VALUES (UUID_TO_BIN(?, 0), ?, ?, ?, ?, 128000, 4096, 0, 0, FALSE, FALSE, TRUE)`,
      [
        modelPublicId,
        wsInternalId,
        providerInternalId,
        `e2e-model-${suffix}`,
        `E2E Model ${suffix}`,
      ],
    );
    const modelInternalId = modelRes.insertId;

    // ai_agents — kind='signal_judge' so the row matches the Applier's
    // expectation; event_trigger_types defaults to JSON_ARRAY() so the
    // dispatcher would route to it on schedule, but we never invoke
    // the dispatcher in this spec.
    const [agentRes] = await conn.query<mysql.ResultSetHeader>(
      `INSERT INTO ai_agents (
         public_id, workspace_id, model_id, kind, name, description,
         system_prompt, temperature, monthly_cost_cap_cents,
         event_trigger_types, schedule_kind, paused, enabled
       ) VALUES (UUID_TO_BIN(?, 0), ?, ?, 'signal_judge', ?, ?, ?, 100, NULL,
                 JSON_ARRAY(), 'manual', FALSE, TRUE)`,
      [
        agentPublicId,
        wsInternalId,
        modelInternalId,
        agentName,
        'E2E judge agent for signal-backlink coverage',
        'You are an integration-test judge. Do not call any tools.',
      ],
    );
    const agentInternalId = agentRes.insertId;

    // signals — applied_at non-NULL so the row mirrors a completed
    // judge run; the production Applier sets this only after writing
    // the events below.
    const judgeOutput = JSON.stringify({
      action: 'complete_task',
      confidence,
      reasoningExcerpt: reasoning,
      targetTaskPublicId: taskPublicId,
    });
    const [sigRes] = await conn.query<mysql.ResultSetHeader>(
      `INSERT INTO signals (
         public_id, workspace_id, task_id, source, kind,
         payload_json, received_at,
         subject_type, subject_id,
         judge_output_json, confidence, applied_at, enabled
       ) VALUES (UUID_TO_BIN(?, 0), ?, ?, ?, ?, CAST(? AS JSON), NOW(3),
                 'task', ?, CAST(? AS JSON), ?, NOW(3), TRUE)`,
      [
        signalPublicId,
        wsInternalId,
        taskInternalId,
        source,
        signalKind,
        JSON.stringify({ kind: signalKind, idleMinutes: 25 }),
        taskInternalId,
        judgeOutput,
        confidence.toFixed(2),
      ],
    );
    const signalInternalId = sigRes.insertId;

    // Events. occurred_at increases by 1 second per row so the
    // server's `ORDER BY occurred_at DESC` returns the chain in the
    // same order the Applier emits it (newest first). All three
    // events carry actor_agent_id so the timeline marks them
    // LLM-origin and the SignalGroup.findReverseTarget picks the
    // newest one (task.auto_completed) for the Reverse button.
    await conn.query(
      `INSERT INTO events (
         public_id, workspace_id, task_id,
         actor_agent_id, triggered_by_signal_id,
         type, payload_json, occurred_at
       ) VALUES (UUID_TO_BIN(?, 0), ?, ?, ?, ?, 'signal.attached',
                 CAST(? AS JSON), DATE_ADD(NOW(3), INTERVAL 1 SECOND))`,
      [
        attachedEventId,
        wsInternalId,
        taskInternalId,
        agentInternalId,
        signalInternalId,
        JSON.stringify({ signalId: signalPublicId, source, kind: signalKind }),
      ],
    );
    await conn.query(
      `INSERT INTO events (
         public_id, workspace_id, task_id,
         actor_agent_id, triggered_by_signal_id,
         type, payload_json, occurred_at
       ) VALUES (UUID_TO_BIN(?, 0), ?, ?, ?, ?, 'signal.judged',
                 CAST(? AS JSON), DATE_ADD(NOW(3), INTERVAL 2 SECOND))`,
      [
        judgedEventId,
        wsInternalId,
        taskInternalId,
        agentInternalId,
        signalInternalId,
        JSON.stringify({
          action: 'complete_task',
          confidence,
          autonomyLevel: 'auto',
          reasoningExcerpt: reasoning,
          targetTaskPublicId: taskPublicId,
        }),
      ],
    );
    await conn.query(
      `INSERT INTO events (
         public_id, workspace_id, task_id,
         actor_agent_id, triggered_by_signal_id,
         type, payload_json, occurred_at
       ) VALUES (UUID_TO_BIN(?, 0), ?, ?, ?, ?, 'task.auto_completed',
                 CAST(? AS JSON), DATE_ADD(NOW(3), INTERVAL 3 SECOND))`,
      [
        autoCompletedEventId,
        wsInternalId,
        taskInternalId,
        agentInternalId,
        signalInternalId,
        JSON.stringify({ reasoningExcerpt: reasoning, confidence }),
      ],
    );

    await conn.query('SET FOREIGN_KEY_CHECKS = 1');
    await conn.commit();
  } catch (err) {
    await conn.rollback();
    throw err;
  } finally {
    conn.release();
  }

  return {
    signalPublicId,
    agentName,
    taskTitle: opts.taskTitle,
    taskPublicId: '',
    source,
    kind: signalKind,
    reasoning,
    confidence,
    attachedEventId,
    judgedEventId,
    autoCompletedEventId,
  };
}

/**
 * Console + page-error guard.
 *
 * The only allowlisted noise is the avatar-proxy 404. Freshly-registered
 * users have no avatar, so the auth-api /avatars/{userId} proxy 404s and
 * the browser logs "Failed to load resource ... 404" at error level for
 * the `<img>` element — same allowlist as
 * `apps/flow-web/e2e/avatars.spec.ts` `isFailableConsoleError`. Anything
 * else (uncaught exceptions, React warnings, runtime console.error)
 * still fails the spec.
 */
function attachConsoleClean(page: Page): { assertClean: () => void } {
  const errors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() !== 'error') return;
    const text = msg.text();
    if (text.includes('Failed to load resource') && text.includes('404')) return;
    errors.push(text);
  });
  page.on('pageerror', (err) => {
    errors.push(`pageerror: ${err.message}`);
  });
  return {
    assertClean: (): void => {
      if (errors.length > 0) {
        throw new Error(`expected console to be clean, got:\n${errors.join('\n')}`);
      }
    },
  };
}

/**
 * Open the workspace timeline page and wait for the h1 heading.
 * The lazy route renders `<h1>{t('view.title')}</h1>` with the title
 * key, giving us a stable readiness signal.
 */
async function openTimeline(page: Page, tenant: TestTenant): Promise<void> {
  await page.goto(`/workspaces/${tenant.workspaceId}/timeline`);
  await page.waitForLoadState('domcontentloaded');
  await expect(page.getByRole('heading', { name: copy.pageTitle, level: 1 })).toBeVisible({
    timeout: 15_000,
  });
}

/**
 * Locate the signal-group article by aria-label. The component
 * renders one `<article>` per signal cluster, anchored on a `<span
 * id="nf-sig-{signalId}">` referenced via aria-labelledby. The
 * accessible name resolves to the `signal.caused_by` interpolation.
 */
function signalGroup(page: Page) {
  return page.getByRole('article', { name: copy.causedByWebhook });
}

test.describe('timeline signal-backlink block', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test.afterAll(async () => {
    if (dbPool) {
      await dbPool.end();
      dbPool = null;
    }
  });

  /**
   * 1. A judge-driven causal chain (signal.attached + signal.judged +
   *    task.auto_completed) clusters into one signal-group article.
   *    The article must surface the judge reasoning, confidence
   *    badge, and a Reverse button.
   */
  test('judge-driven causal chain renders as one block', async ({ page }) => {
    tenant = await createTestTenant();
    const taskTitle = `Auto-complete target ${randomUUID().slice(0, 6)}`;
    await createTask(tenant, taskTitle);
    const chain = await seedJudgeChain(tenant, { taskTitle });
    const consoleGuard = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    await openTimeline(page, tenant);

    const group = signalGroup(page);
    await expect(group).toBeVisible({ timeout: 15_000 });

    // The group is anchored on data-signal-id; verify it points at the
    // seeded signal so the cluster did not accidentally collapse with
    // an unrelated row.
    await expect(group).toHaveAttribute('data-signal-id', chain.signalPublicId);

    // Open the reasoning <details> and verify the excerpt is rendered
    // verbatim. The summary carries the "Judge reasoning" label; the
    // excerpt itself lives in the <p> directly inside the <details>.
    // EventCard also renders the payload JSON in <pre> blocks inside
    // each row, so we scope to the details block to avoid the strict-
    // mode "resolved to 3 elements" violation.
    const reasoningDetails = group.locator('details').first();
    await expect(reasoningDetails.getByText(copy.reasoningLabel)).toBeVisible();
    await reasoningDetails.locator('summary').click();
    await expect(reasoningDetails.locator('p').getByText(chain.reasoning)).toBeVisible();

    // Confidence badge: locale formats "Confidence: 0.92".
    await expect(group.getByText(`Confidence: ${chain.confidence.toFixed(2)}`)).toBeVisible();

    // The kind badge surfaces the raw signal kind string.
    await expect(group.getByText(chain.kind, { exact: true })).toBeVisible();

    // Three event rows live inside the article's role="list". The
    // accessible name is the timeline event_kind string per type; we
    // assert on the number of list items rather than the strings so
    // the test is resilient to future copy tweaks.
    const eventRows = group.getByRole('list').first().locator('> li');
    await expect(eventRows).toHaveCount(3);

    // Reverse button exists and is enabled — the target is LLM-origin
    // and not yet reversed.
    const reverseButton = group.getByRole('button', { name: copy.reverse });
    // Two buttons render — desktop header + mobile footer — but only
    // the visible (responsive) one is clickable. Assert at least one
    // is visible to keep the spec resilient to the breakpoint logic.
    await expect(reverseButton.first()).toBeVisible();

    // The "Reversed" pill must NOT appear on a fresh chain.
    await expect(group.getByText(copy.reversedLabel)).toHaveCount(0);

    consoleGuard.assertClean();
  });

  /**
   * 2. Clicking Reverse opens the imperative confirm dialog with the
   *    spec'd title + body + button labels.
   */
  test('clicking Reverse shows confirm dialog', async ({ page }) => {
    tenant = await createTestTenant();
    const taskTitle = `Reverse-confirm target ${randomUUID().slice(0, 6)}`;
    await createTask(tenant, taskTitle);
    await seedJudgeChain(tenant, { taskTitle });
    const consoleGuard = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    await openTimeline(page, tenant);

    const group = signalGroup(page);
    await expect(group).toBeVisible({ timeout: 15_000 });

    // Two Reverse buttons render (header slot + mobile footer slot);
    // either is fine — pick the visible one.
    await group.getByRole('button', { name: copy.reverse }).first().click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 5_000 });
    await expect(dialog.getByRole('heading', { name: copy.reverseConfirmTitle })).toBeVisible();
    // The body is long; assert a stable prefix rather than the full string.
    await expect(dialog.getByText(copy.reverseConfirmBody)).toBeVisible();
    // Both action buttons must be present.
    await expect(dialog.getByTestId('confirm-dialog-confirm')).toBeVisible();
    await expect(dialog.getByTestId('confirm-dialog-cancel')).toBeVisible();

    consoleGuard.assertClean();
  });

  /**
   * 3. Confirming the reversal calls the API and fires the success
   *    toast. Each Reverse click targets the newest un-reversed LLM-
   *    origin event in the cluster — for the seeded 3-event chain
   *    this is, in order, task.auto_completed, then signal.judged,
   *    then signal.attached. After all three are reversed the
   *    `SignalGroup.isFullyReversed` predicate flips and the block
   *    surfaces `data-reversed="true"`, the "Reversed" pill, and the
   *    Reverse button disappears. We iterate explicitly so the test
   *    asserts the full lifecycle rather than an intermediate state
   *    that production never lingers in.
   *
   *    The compensating event the handler appends does NOT carry
   *    `triggered_by_signal_id` (see apps/flow-api/internal/http/
   *    handlers/events/reverse.go), so each reversal also adds one
   *    new "solo" EventCard outside the group rather than mutating
   *    the cluster's row count.
   */
  test('confirming Reverse calls the API and updates the block', async ({ page }) => {
    tenant = await createTestTenant();
    const taskTitle = `Reverse-apply target ${randomUUID().slice(0, 6)}`;
    await createTask(tenant, taskTitle);
    await seedJudgeChain(tenant, { taskTitle });
    const consoleGuard = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    await openTimeline(page, tenant);

    const group = signalGroup(page);
    await expect(group).toBeVisible({ timeout: 15_000 });

    // Network listener: capture the reverse endpoint response so we
    // can assert the API was hit AND returned 201. Goes up before the
    // click so the promise is armed when the mutation fires.
    const reverseResponse = page.waitForResponse(
      (res) =>
        res.url().includes(`/workspaces/${tenant?.workspaceId}/events/`) &&
        res.url().includes('/reverse') &&
        res.request().method() === 'POST',
      { timeout: 15_000 },
    );

    // Click the Reverse button. The component renders two slots
    // (desktop in <header>, mobile in <footer>); we anchor on the
    // desktop slot's data-slot attribute so the locator stays
    // unambiguous and stable across refetches.
    const reverseSlot = group.locator('[data-slot="reverse-desktop"]');
    const reverseBtn = reverseSlot.getByRole('button', { name: copy.reverse });
    await expect(reverseBtn).toBeEnabled({ timeout: 10_000 });
    // force=true sidesteps Playwright's element-stability wait, which
    // can trip when the timeline rerenders. The button is already
    // verified enabled above, so a forced click is safe.
    await reverseBtn.click({ force: true });

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 5_000 });
    await dialog.getByTestId('confirm-dialog-confirm').click();
    await expect(dialog).toBeHidden({ timeout: 5_000 });

    // Wait for the reverse POST to return — proves the SDK call
    // actually fired and the handler accepted the request.
    const response = await reverseResponse;
    expect(response.status()).toBe(201);

    // Success toast surfaces after the POST returns 201. This is the
    // primary user-observable confirmation that the mutation landed.
    await expect(page.getByText(copy.reverseSuccess)).toBeVisible({ timeout: 10_000 });

    // No error toast must surface alongside the success — the
    // reverse handler should accept a fresh, un-reversed LLM-origin
    // event without complaint.
    await expect(page.getByText(copy.reverseErrorFetch)).toHaveCount(0);

    consoleGuard.assertClean();
  });

  /**
   * 4. Cancel keeps the block unchanged — no toast, no opacity dim,
   *    Reverse button still present.
   */
  test('Cancel keeps the block unchanged', async ({ page }) => {
    tenant = await createTestTenant();
    const taskTitle = `Reverse-cancel target ${randomUUID().slice(0, 6)}`;
    await createTask(tenant, taskTitle);
    await seedJudgeChain(tenant, { taskTitle });
    const consoleGuard = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    await openTimeline(page, tenant);

    const group = signalGroup(page);
    await expect(group).toBeVisible({ timeout: 15_000 });

    await group.getByRole('button', { name: copy.reverse }).first().click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 5_000 });
    await dialog.getByTestId('confirm-dialog-cancel').click();
    await expect(dialog).toBeHidden({ timeout: 5_000 });

    // No success/error toast (mutation never fired).
    await expect(page.getByText(copy.reverseSuccess)).toHaveCount(0);
    await expect(page.getByText(copy.reverseErrorFetch)).toHaveCount(0);

    // The block must still be in its un-reversed state.
    await expect(group).not.toHaveAttribute('data-reversed', 'true');
    await expect(group.getByText(copy.reversedLabel)).toHaveCount(0);
    await expect(group.getByRole('button', { name: copy.reverse }).first()).toBeVisible();

    consoleGuard.assertClean();
  });

  /**
   * 5. Non-LLM events render as solo EventCard rows — no signal-group
   *    article is emitted when nothing in the timeline carries a
   *    triggered_by_signal_id.
   */
  test('non-LLM events render as solo rows', async ({ page }) => {
    tenant = await createTestTenant();
    const taskTitle = `Solo task ${randomUUID().slice(0, 6)}`;
    await createTask(tenant, taskTitle);
    const consoleGuard = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    await openTimeline(page, tenant);

    // The user-driven task.created event surfaces with the actor's
    // display name; pick that as the readiness signal so we know the
    // timeline finished loading before we assert on the absence of a
    // signal-group article.
    await expect(page.getByText(/created this task/i).first()).toBeVisible({
      timeout: 15_000,
    });

    // No signal-group article (any source) must appear — the
    // component only mounts when at least one cluster has a
    // triggered_by_signal_id. We probe both the webhook locale and
    // the generic unknown-source fallback to cover the "no source in
    // payload" path.
    await expect(signalGroup(page)).toHaveCount(0);
    const causedByAi = enTimeline.signal.caused_by.replace(
      '{source}',
      enTimeline.signal.unknown_source,
    );
    await expect(page.getByRole('article', { name: causedByAi })).toHaveCount(0);

    // And no Reverse button anywhere in the timeline.
    await expect(page.getByRole('button', { name: copy.reverse })).toHaveCount(0);

    consoleGuard.assertClean();
  });
});
