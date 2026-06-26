/**
 * Autonomy Matrix E2E — Phase 4 / A2 of release-8-signals-and-judge-loop.
 *
 * Covers `apps/flow-web/src/features/ai/autonomy/autonomy-matrix.tsx`
 * rendered as a section inside `auto-action-settings.tsx`, mounted at
 * `/workspaces/{wsId}/settings/auto-actions`.
 *
 * The matrix renders one row per signal_kind (8 today, sourced from the
 * SDK's SIGNAL_KINDS registry). Each row exposes a SegmentedControl with
 * three modes — suggest / draft / auto — that map to the operator-picked
 * `autonomyLevel` override on the underlying auto-action rules. Saving a
 * row fans out into one PATCH entry per rule kind so the underlying
 * `(kind, signalKind)` pairs all carry the same override.
 *
 * Cases:
 *   1. matrix renders all 8 signal kinds with the three labelled modes.
 *   2. picking Auto in a row → Save (1) → success toast → reload keeps
 *      Auto active for that row.
 *   3. picking modes in two rows → Save reflects count=2 → Reset clears
 *      dirty state → both rows return to their pre-edit segments.
 *
 *   (Test 4 — Save-error rollback — is intentionally omitted; forcing a
 *   500 mid-test would require either an API mock or backend fault
 *   injection. The harness has neither, and CLAUDE.md rule #7 forbids
 *   the former.
 *   TODO: error-path coverage requires harness extension for backend
 *   fault injection.)
 *
 * Pattern follows ai-metrics.spec.ts: each test creates its own tenant
 * via REST + injectAuth + direct URL navigation. Parallel-safe.
 */

import { expect, type Locator, type Page, test } from '@playwright/test';

import enAi from '../../locales/en/ai.json' with { type: 'json' };
import enSignalKinds from '../../locales/en/signal-kinds.json' with { type: 'json' };
import { cleanupTenant, createTestTenant, injectAuth, type TestTenant } from '../fixtures/tenant';

const copy = {
  matrixTitle: enAi.autonomy.matrix.title,
  headerKind: enAi.autonomy.matrix.header.kind,
  headerMode: enAi.autonomy.matrix.header.mode,
  modeSuggest: enAi.autonomy.mode.suggest,
  modeDraft: enAi.autonomy.mode.draft,
  modeAuto: enAi.autonomy.mode.auto,
  reset: enAi.autonomy.matrix.reset,
  saveZero: enAi.autonomy.matrix.save_zero,
  changedToast: enAi.autonomy.matrix.changed_toast,
  discordPresenceLabel: enSignalKinds['signalKinds.discord.presence.label'],
  slackPresenceLabel: enSignalKinds['signalKinds.slack.presence.label'],
} as const;

/**
 * Build the "Save changes ({count})" copy with `count` substituted.
 * The i18n value is a literal `"Save changes ({count})"` (no plural
 * variants), so a string replace is sufficient.
 */
function saveLabel(count: number): string {
  return enAi.autonomy.matrix.save.replace('{count}', String(count));
}

/**
 * Navigates to the auto-actions settings page and waits for the matrix
 * section to mount.
 *
 * The matrix renders a `<table aria-label="Autonomy by signal kind">`
 * which gives us a stable accessible name to wait on without coupling to
 * the surrounding workspace settings layout.
 */
async function openAutonomyMatrix(page: Page, tenant: TestTenant): Promise<Locator> {
  await page.goto(`/workspaces/${tenant.workspaceId}/settings/auto-actions`);
  await page.waitForLoadState('domcontentloaded');
  const matrix = page.getByRole('table', { name: copy.matrixTitle });
  await expect(matrix).toBeVisible({ timeout: 15_000 });
  return matrix;
}

/**
 * Returns the SegmentedControl radiogroup for `signalKindLabel`.
 *
 * The component passes `ariaLabel={t('ai:autonomy.matrix.row_aria', { kind: kindLabel })}`
 * to its `<div role="radiogroup">`, which resolves to e.g.
 * "Autonomy level for Discord presence". Anchoring on that name lets us
 * scope segment lookups to a single row without depending on the table's
 * row index (rows are sorted alphabetically by kind in the SDK registry).
 */
function rowFor(matrixPage: Page, signalKindLabel: string): Locator {
  return matrixPage.getByRole('radiogroup', {
    name: `Autonomy level for ${signalKindLabel}`,
  });
}

/**
 * Returns a console listener that fails the test if any console-error
 * message fires. Suppresses well-known third-party noise (none expected
 * here, but we keep the filter explicit so future additions are
 * traceable).
 */
function attachConsoleClean(page: Page): { assertClean: () => void } {
  const errors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() !== 'error') return;
    const text = msg.text();
    // Intentional allowlist is empty — any console.error fails the test.
    errors.push(text);
  });
  page.on('pageerror', (err) => {
    errors.push(`pageerror: ${err.message}`);
  });
  return {
    assertClean: () => {
      if (errors.length > 0) {
        throw new Error(`expected console to be clean, got:\n${errors.join('\n')}`);
      }
    },
  };
}

test.describe('autonomy matrix', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  /**
   * 1. Matrix mounts, shows the table header, has at least 8 rows (one
   *    per signal kind from the SDK registry), and every row exposes
   *    Suggest / Draft / Auto segments. Console stays clean during the
   *    initial render.
   */
  test('renders all signal kinds with SegmentedControl options', async ({ page }) => {
    tenant = await createTestTenant();
    const console = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    const matrix = await openAutonomyMatrix(page, tenant);

    // Column headers — sanity check that we landed on the right table.
    await expect(matrix.getByRole('columnheader', { name: copy.headerKind })).toBeVisible();
    await expect(matrix.getByRole('columnheader', { name: copy.headerMode })).toBeVisible();

    // One row per signal kind. The SDK registry currently ships 8 kinds;
    // assert at least 8 so the test stays valid if more get added.
    const rowGroups = matrix.getByRole('radiogroup');
    await expect(rowGroups).toHaveCount(8, { timeout: 5_000 });

    // Every row carries the three labelled segments. Check on the
    // discord.presence row as a representative sample — exhaustive
    // per-row assertions would just multiply by the registry size
    // without adding signal.
    const row = rowFor(page, copy.discordPresenceLabel);
    await expect(row.getByRole('radio', { name: copy.modeSuggest })).toBeVisible();
    await expect(row.getByRole('radio', { name: copy.modeDraft })).toBeVisible();
    await expect(row.getByRole('radio', { name: copy.modeAuto })).toBeVisible();

    // Save button is disabled and shows the no-changes label on a fresh
    // mount (no overrides yet).
    await expect(page.getByRole('button', { name: copy.saveZero })).toBeDisabled();

    console.assertClean();
  });

  /**
   * 2. Pick "Auto" in the discord.presence row → Save button label
   *    updates to "Save changes (1)" → click Save → success toast →
   *    reload → "Auto" segment is still active for the same row.
   *
   * Note: discord.presence has `autonomyDefault: 'suggest'` per the SDK
   *    registry, so on a fresh tenant the SegmentedControl initially
   *    paints Suggest as the focal point (with the muted "Default:
   *    Suggest" hint visible). Picking Auto creates an explicit override
   *    that the backend should persist across the reload.
   */
  test('changing a row to Auto persists across reload', async ({ page }) => {
    tenant = await createTestTenant();
    const console = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    await openAutonomyMatrix(page, tenant);

    const row = rowFor(page, copy.discordPresenceLabel);
    await row.getByRole('radio', { name: copy.modeAuto }).click();

    // The Save button label is computed from `dirtyCount`. One dirty row
    // -> "Save changes (1)".
    const saveButton = page.getByRole('button', { name: saveLabel(1) });
    await expect(saveButton).toBeEnabled({ timeout: 5_000 });
    await saveButton.click();

    // Success toast.
    await expect(page.getByText(copy.changedToast)).toBeVisible({ timeout: 5_000 });

    // After a save, dirtyCount resets to 0 so the button label flips back
    // to the no-changes copy.
    await expect(page.getByRole('button', { name: copy.saveZero })).toBeDisabled({
      timeout: 5_000,
    });

    // Reload and re-anchor on the matrix. The persisted override should
    // make Auto the active segment without any local edits.
    await page.reload();
    await openAutonomyMatrix(page, tenant);

    const reloadedRow = rowFor(page, copy.discordPresenceLabel);
    await expect(reloadedRow.getByRole('radio', { name: copy.modeAuto })).toHaveAttribute(
      'aria-checked',
      'true',
    );

    console.assertClean();
  });

  /**
   * 3. Pick Draft in discord.presence and Auto in slack.presence → Save
   *    label shows count=2 → click Reset → Save returns to the disabled
   *    no-changes state and both rows revert to their pre-edit segments
   *    (Suggest, the YAML default for both).
   */
  test('Reset discards local edits before save', async ({ page }) => {
    tenant = await createTestTenant();
    const console = attachConsoleClean(page);

    await injectAuth(page.context(), tenant);
    await openAutonomyMatrix(page, tenant);

    const discord = rowFor(page, copy.discordPresenceLabel);
    const slack = rowFor(page, copy.slackPresenceLabel);

    // Pre-condition: both rows default to Suggest (the YAML default).
    // The SegmentedControl paints that as the focal segment but
    // `aria-checked` only flips true once a value is actually selected
    // server-side or locally; both are unset on a fresh tenant. We
    // assert the post-Reset state below.

    await discord.getByRole('radio', { name: copy.modeDraft }).click();
    await expect(page.getByRole('button', { name: saveLabel(1) })).toBeEnabled();

    await slack.getByRole('radio', { name: copy.modeAuto }).click();
    await expect(page.getByRole('button', { name: saveLabel(2) })).toBeEnabled({
      timeout: 5_000,
    });

    // Reset clears the dirty map.
    await page.getByRole('button', { name: copy.reset }).click();

    // Save button returns to the disabled no-changes state.
    await expect(page.getByRole('button', { name: copy.saveZero })).toBeDisabled({
      timeout: 5_000,
    });

    // Both rows revert: their previously selected segments lose
    // aria-checked="true" (they were only locally dirty, never saved).
    await expect(discord.getByRole('radio', { name: copy.modeDraft })).toHaveAttribute(
      'aria-checked',
      'false',
    );
    await expect(slack.getByRole('radio', { name: copy.modeAuto })).toHaveAttribute(
      'aria-checked',
      'false',
    );

    console.assertClean();
  });
});
