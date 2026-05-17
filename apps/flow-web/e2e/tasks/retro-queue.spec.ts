/**
 * Retro draft queue E2E — Phase 6 / L2 of
 * docs/plan/release-8-signals-and-judge-loop.md.
 *
 * Covers `apps/flow-web/src/features/tasks/retro-drafts/retro-drafts-page.tsx`
 * mounted at `/workspaces/{wsId}/tasks/drafts`. The page lists every
 * retrospective task drafted by the signal_judge Applier — tasks linked
 * back to a source task via a `task_dependencies` row of `kind='retro_of'`.
 *
 * Backend contract:
 *   - GET  /workspaces/{wsId}/tasks/drafts?reason=retro
 *   - GET  /tasks/{taskId}/dependencies      (used by Accept)
 *   - DELETE /tasks/{taskId}/dependencies/{depId}  (Accept drops retro_of)
 *   - POST /tasks/{taskId}/archive           (Discard)
 *
 * Seeding strategy: the public dependency-create endpoint constrains
 * `kind` to `enum:"blocks,relates,duplicates,subtask_of"` (see
 * apps/flow-api/internal/http/handlers/tasks/types.go:AddTaskDependencyBody)
 * — `retro_of` is reserved for the Applier and rejected at the boundary.
 * To exercise the UI we therefore mirror the Go-side helper in
 * apps/flow-api/tests/e2e/tasks_drafts_test.go and write directly into
 * `tasks` + `task_dependencies` via mysql2. Each test owns a fresh
 * tenant so the parallel-safe contract is preserved.
 *
 * The seed deliberately omits the `task.retro.drafted` event row. The
 * handler treats agent attribution as best-effort (sql.ErrNoRows leaves
 * the optional fields unset), so the UI falls back to the
 * `tasks.retro.queue.created_anon` copy — "Drafted by AI · {when}".
 * Skipping the event keeps the seed lean and avoids coupling the spec
 * to the ai_providers / ai_models / ai_agents fixture chain.
 *
 * Cases:
 *   1. Queue page renders all seeded drafts with their source backlinks.
 *   2. Accept removes the row, fires the success toast, and the row
 *      stays gone after a reload.
 *   3. Discard requires confirmation, archives the task, fires the
 *      success toast, and persists after reload.
 *   4. Discard cancel keeps the row and emits no toast.
 *   5. Empty queue shows the explanatory empty-state copy.
 */

import { randomUUID } from 'node:crypto';

import { type Page, expect, test } from '@playwright/test';
import mysql, { type Pool } from 'mysql2/promise';

import enCommon from '../../locales/en/common.json' with { type: 'json' };
import { type TestTenant, cleanupTenant, createTestTenant, injectAuth } from '../fixtures/tenant';

const copy = {
  title: enCommon.tasks.retro.queue.title,
  empty: enCommon.tasks.retro.queue.empty,
  accept: enCommon.tasks.retro.queue.accept,
  discard: enCommon.tasks.retro.queue.discard,
  acceptedToast: enCommon.tasks.retro.queue.accepted_toast,
  discardedToast: enCommon.tasks.retro.queue.discarded_toast,
  discardConfirmTitle: enCommon.tasks.retro.queue.discard_confirm.title,
  discardConfirmConfirm: enCommon.tasks.retro.queue.discard_confirm.confirm,
  discardConfirmCancel: enCommon.tasks.retro.queue.discard_confirm.cancel,
} as const;

/**
 * Single shared connection pool — keeps mysql2 from opening a fresh TCP
 * handshake per seed call. The dev compose stack exposes MySQL on
 * 127.0.0.1:3306; override via NF_DB_* env in CI if the host shape ever
 * changes. The pool is created lazily on first use because module-level
 * side effects in Playwright specs run inside every worker.
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
    // The handler reads `description` via the standard utf8mb4 column;
    // mysql2's default charset is latin1, so pin to the server collation.
    charset: 'utf8mb4_0900_ai_ci',
  });
  return dbPool;
}

/**
 * Result of {@link seedRetroDraft}. Returned so the spec can assert on
 * the visible titles and tear down deterministically.
 */
interface SeededRetroDraft {
  sourceTitle: string;
  draftTitle: string;
  /** Hyphenated UUID v7 of the new retro task, matches the route URL. */
  draftPublicId: string;
  /** Hyphenated UUID v7 of the source task. */
  sourcePublicId: string;
}

/**
 * Mirrors apps/flow-api/tests/e2e/tasks_drafts_test.go's seedRetroDraft.
 *
 * Performs four writes in one transaction:
 *   1. The source task ("the completed task that prompted a retro").
 *   2. The retro draft task (`derived_state='open'`, like any task — the
 *      "draft" semantics live on the dependency edge, not the task row).
 *   3. The `task_dependencies` row with `kind='retro_of'`:
 *        from = the retro draft, to = the source task.
 *
 * `task_number` is allocated by SELECT MAX + 1 inside the tx, mirroring
 * AssignTaskNumber's behaviour. Per-project sequencing is fine because
 * each tenant has its own project from createTestTenant.
 *
 * Optional event row is deliberately skipped — see the file header.
 */
async function seedRetroDraft(tenant: TestTenant, suffix: string): Promise<SeededRetroDraft> {
  const sourceTitle = `Source ${suffix}`;
  const draftTitle = `Retro: ${sourceTitle}`;
  const sourcePublicId = randomUUID();
  const draftPublicId = randomUUID();
  const depPublicId = randomUUID();

  const conn = await getDb().getConnection();
  try {
    await conn.beginTransaction();

    // Resolve internal ids for workspace + project from their public ids.
    const [wsRows] = await conn.query<mysql.RowDataPacket[]>(
      'SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1',
      [tenant.workspaceId],
    );
    if (wsRows.length === 0) {
      throw new Error(`seedRetroDraft: workspace ${tenant.workspaceId} not found`);
    }
    const wsInternalId = wsRows[0]?.id as number;

    const [prjRows] = await conn.query<mysql.RowDataPacket[]>(
      `SELECT id FROM projects
         WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
      [wsInternalId, tenant.projectId],
    );
    if (prjRows.length === 0) {
      throw new Error(`seedRetroDraft: project ${tenant.projectId} not found`);
    }
    const prjInternalId = prjRows[0]?.id as number;

    // task_number is per-project — mirror AssignTaskNumber by reading
    // MAX inside the transaction. Concurrent seeds against the same
    // project would race, but each tenant owns its own project so
    // parallel specs never collide.
    const [maxRows] = await conn.query<mysql.RowDataPacket[]>(
      'SELECT COALESCE(MAX(task_number), 0) AS max_n FROM tasks WHERE project_id = ?',
      [prjInternalId],
    );
    const baseTaskNumber = Number(maxRows[0]?.max_n ?? 0);

    // Source task.
    const [srcInsert] = await conn.query<mysql.ResultSetHeader>(
      `INSERT INTO tasks (
         public_id, workspace_id, project_id, task_number,
         title, description, derived_state, visibility
       ) VALUES (UUID_TO_BIN(?, 0), ?, ?, ?, ?, ?, 'open', 'public')`,
      [
        sourcePublicId,
        wsInternalId,
        prjInternalId,
        baseTaskNumber + 1,
        sourceTitle,
        'Original task body',
      ],
    );
    const sourceInternalId = srcInsert.insertId;

    // Retro draft task.
    const [draftInsert] = await conn.query<mysql.ResultSetHeader>(
      `INSERT INTO tasks (
         public_id, workspace_id, project_id, task_number,
         title, description, derived_state, visibility
       ) VALUES (UUID_TO_BIN(?, 0), ?, ?, ?, ?, ?, 'open', 'public')`,
      [
        draftPublicId,
        wsInternalId,
        prjInternalId,
        baseTaskNumber + 2,
        draftTitle,
        'Drafted retrospective body',
      ],
    );
    const draftInternalId = draftInsert.insertId;

    // retro_of edge: from = draft, to = source.
    await conn.query(
      `INSERT INTO task_dependencies (
         public_id, workspace_id, from_task_id, to_task_id, kind, enabled
       ) VALUES (UUID_TO_BIN(?, 0), ?, ?, ?, 'retro_of', TRUE)`,
      [depPublicId, wsInternalId, draftInternalId, sourceInternalId],
    );

    await conn.commit();
  } catch (err) {
    await conn.rollback();
    throw err;
  } finally {
    conn.release();
  }

  return { sourceTitle, draftTitle, draftPublicId, sourcePublicId };
}

/**
 * Attach a console listener that fails the test on any console.error or
 * page error. The empty allowlist is intentional — any error fails the
 * test so we never accidentally ship a regression hidden behind a noisy
 * stack trace.
 */
function attachConsoleClean(page: Page): { assertClean: () => void } {
  const errors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() !== 'error') return;
    errors.push(msg.text());
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
 * Navigate to the retro queue and wait for the page header. The h1 has
 * `id="retro-drafts-title"` which the main element references via
 * `aria-labelledby`, giving us a stable accessible-name anchor.
 */
async function openQueue(page: Page, tenant: TestTenant): Promise<void> {
  await page.goto(`/workspaces/${tenant.workspaceId}/tasks/drafts`);
  await page.waitForLoadState('domcontentloaded');
  await expect(page.getByRole('heading', { name: copy.title, level: 1 })).toBeVisible({
    timeout: 15_000,
  });
}

test.describe('retro draft queue', () => {
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
   * 1. Two seeded drafts render, each row showing its draft title plus
   *    a "Linked to: {source title}" backlink. The empty state must NOT
   *    appear.
   */
  test('queue page renders retro drafts with source backlink', async ({ page }) => {
    tenant = await createTestTenant();
    const suffix = randomUUID().slice(0, 8);
    const draftA = await seedRetroDraft(tenant, `${suffix}-A`);
    const draftB = await seedRetroDraft(tenant, `${suffix}-B`);
    const consoleGuard = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    await openQueue(page, tenant);

    // Both draft titles render as links to the task detail page.
    await expect(page.getByRole('link', { name: draftA.draftTitle, exact: true })).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByRole('link', { name: draftB.draftTitle, exact: true })).toBeVisible();

    // Backlink copy. The i18n string is "Linked to: {sourceTitle}".
    await expect(page.getByText(`Linked to: ${draftA.sourceTitle}`)).toBeVisible();
    await expect(page.getByText(`Linked to: ${draftB.sourceTitle}`)).toBeVisible();

    // The "Drafted by AI · …" attribution surfaces for every row. We
    // omit the agent fields server-side so the anon variant renders.
    await expect(page.getByText(/^Drafted by AI · /).first()).toBeVisible();

    // Two Accept buttons + two Discard buttons (one per row).
    await expect(page.getByRole('button', { name: copy.accept })).toHaveCount(2);
    await expect(page.getByRole('button', { name: copy.discard })).toHaveCount(2);

    // The empty-state copy must not appear when drafts are present.
    await expect(page.getByText(copy.empty)).toHaveCount(0);

    consoleGuard.assertClean();
  });

  /**
   * 2. Accept removes the `retro_of` edge → row drops from the queue,
   *    success toast appears, and a reload confirms server persistence
   *    (no longer in the GET response).
   */
  test('Accept removes the row and toasts success', async ({ page }) => {
    tenant = await createTestTenant();
    const suffix = randomUUID().slice(0, 8);
    const draft = await seedRetroDraft(tenant, suffix);
    const consoleGuard = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    await openQueue(page, tenant);

    const titleLink = page.getByRole('link', { name: draft.draftTitle, exact: true });
    await expect(titleLink).toBeVisible({ timeout: 10_000 });

    // Scope to the Card article housing the draft so we click the
    // right Accept button if future tests grow the row count.
    const row = page.getByRole('article').filter({ hasText: draft.draftTitle });
    await row.getByRole('button', { name: copy.accept }).click();

    await expect(page.getByText(copy.acceptedToast)).toBeVisible({ timeout: 10_000 });

    // Optimistic update + onSettled invalidation should drop the row.
    await expect(titleLink).toBeHidden({ timeout: 10_000 });

    // Server persistence: reload and confirm the row stays gone. The
    // edge was deleted so the GET no longer surfaces this draft.
    await page.reload();
    await openQueue(page, tenant);
    await expect(page.getByRole('link', { name: draft.draftTitle, exact: true })).toHaveCount(0);
    // With nothing left to triage, the empty state should render.
    await expect(page.getByText(copy.empty)).toBeVisible();

    consoleGuard.assertClean();
  });

  /**
   * 3. Discard prompts via the imperative confirm primitive. Confirming
   *    archives the task (POST /tasks/{id}/archive) — the GET drops the
   *    archived row even with the retro_of edge intact, so it disappears
   *    from the queue and stays gone after reload.
   */
  test('Discard requires confirmation and archives', async ({ page }) => {
    tenant = await createTestTenant();
    const suffix = randomUUID().slice(0, 8);
    const draft = await seedRetroDraft(tenant, suffix);
    const consoleGuard = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    await openQueue(page, tenant);

    const titleLink = page.getByRole('link', { name: draft.draftTitle, exact: true });
    await expect(titleLink).toBeVisible({ timeout: 10_000 });

    const row = page.getByRole('article').filter({ hasText: draft.draftTitle });
    await row.getByRole('button', { name: copy.discard }).click();

    // Confirm dialog. The primitive renders a single role="dialog" host;
    // anchor on its title for the assertion.
    const confirmDialog = page.getByRole('dialog');
    await expect(confirmDialog).toBeVisible({ timeout: 5_000 });
    await expect(
      confirmDialog.getByRole('heading', { name: copy.discardConfirmTitle }),
    ).toBeVisible();

    // The themed Confirm button carries a stable testid.
    await confirmDialog.getByTestId('confirm-dialog-confirm').click();
    await expect(confirmDialog).toBeHidden({ timeout: 5_000 });

    await expect(page.getByText(copy.discardedToast)).toBeVisible({ timeout: 10_000 });
    await expect(titleLink).toBeHidden({ timeout: 10_000 });

    // Reload and confirm server persistence — the archive flips
    // tasks.enabled=FALSE so the drafts query no longer returns this
    // row.
    await page.reload();
    await openQueue(page, tenant);
    await expect(page.getByRole('link', { name: draft.draftTitle, exact: true })).toHaveCount(0);
    await expect(page.getByText(copy.empty)).toBeVisible();

    consoleGuard.assertClean();
  });

  /**
   * 4. Cancelling the discard confirmation keeps the row in place and
   *    must not emit any toast — the mutation never runs.
   */
  test('Discard cancel keeps the row', async ({ page }) => {
    tenant = await createTestTenant();
    const suffix = randomUUID().slice(0, 8);
    const draft = await seedRetroDraft(tenant, suffix);
    const consoleGuard = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    await openQueue(page, tenant);

    const titleLink = page.getByRole('link', { name: draft.draftTitle, exact: true });
    await expect(titleLink).toBeVisible({ timeout: 10_000 });

    const row = page.getByRole('article').filter({ hasText: draft.draftTitle });
    await row.getByRole('button', { name: copy.discard }).click();

    const confirmDialog = page.getByRole('dialog');
    await expect(confirmDialog).toBeVisible({ timeout: 5_000 });

    // Cancel dismisses the dialog without firing the mutation.
    await confirmDialog.getByTestId('confirm-dialog-cancel').click();
    await expect(confirmDialog).toBeHidden({ timeout: 5_000 });

    // Row stays visible — short timeout because cancellation should be
    // immediate; a long wait would mask a regression.
    await expect(titleLink).toBeVisible();
    // No toast — neither success nor error copy should appear. Probe
    // both since the mutation could in principle fire and 4xx.
    await expect(page.getByText(copy.discardedToast)).toHaveCount(0);
    await expect(page.getByText(enCommon.tasks.retro.queue.discard_error)).toHaveCount(0);

    consoleGuard.assertClean();
  });

  /**
   * 5. Empty queue: a brand-new tenant has no retro drafts. The page
   *    must render the empty-state copy and surface zero rows.
   */
  test('empty state when no drafts', async ({ page }) => {
    tenant = await createTestTenant();
    const consoleGuard = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    await openQueue(page, tenant);

    await expect(page.getByText(copy.empty)).toBeVisible({ timeout: 10_000 });

    // No row articles — the empty state replaces the list entirely.
    await expect(page.getByRole('article')).toHaveCount(0);
    // And no Accept / Discard buttons.
    await expect(page.getByRole('button', { name: copy.accept })).toHaveCount(0);
    await expect(page.getByRole('button', { name: copy.discard })).toHaveCount(0);

    consoleGuard.assertClean();
  });
});
