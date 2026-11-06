/**
 * Real browser acceptance coverage for the practical inbox and review paths.
 *
 * All setup and outcome reads use the disposable API. The browser performs
 * each user action, and no provider-backed LLM work is requested: the triage
 * case intentionally exercises the API's deterministic no-provider fallback.
 */

import { writeFile } from 'node:fs/promises';
import { type Browser, expect, type Page, type Response, test } from '@playwright/test';

import enAiSuggestions from '../locales/en/ai-suggestions.json' with { type: 'json' };
import enCommon from '../locales/en/common.json' with { type: 'json' };
import enErrors from '../locales/en/errors.json' with { type: 'json' };
import enInbox from '../locales/en/inbox.json' with { type: 'json' };
import jaAiSuggestions from '../locales/ja/ai-suggestions.json' with { type: 'json' };
import jaCommon from '../locales/ja/common.json' with { type: 'json' };
import jaErrors from '../locales/ja/errors.json' with { type: 'json' };
import jaInbox from '../locales/ja/inbox.json' with { type: 'json' };
import {
  API_BASE_URL,
  AUTH_API_URL,
  cleanupTenant,
  createTask,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

type Locale = 'en' | 'ja';

const copy = {
  en: { ai: enAiSuggestions, common: enCommon, errors: enErrors, inbox: enInbox },
  ja: { ai: jaAiSuggestions, common: jaCommon, errors: jaErrors, inbox: jaInbox },
} as const;

interface JsonRequestOptions {
  method: 'GET' | 'POST' | 'PATCH' | 'DELETE';
  bearer: string;
  body?: unknown;
}

async function requestJson<T>(
  baseUrl: string,
  path: string,
  options: JsonRequestOptions,
): Promise<{ response: Response; body: T }> {
  const response = await fetch(`${baseUrl}${path}`, {
    method: options.method,
    headers: {
      accept: 'application/json',
      authorization: `Bearer ${options.bearer}`,
      ...(options.body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
    ...(options.body === undefined ? {} : { body: JSON.stringify(options.body) }),
  });
  const text = await response.text();
  let body = {} as T;
  if (text) body = JSON.parse(text) as T;
  if (!response.ok) {
    throw new Error(`${options.method} ${path} -> ${response.status} ${text}`);
  }
  return { response, body };
}

async function createManualSignal(
  tenant: TestTenant,
  title: string,
  body: string,
): Promise<string> {
  const { body: signal } = await requestJson<{ id: string }>(API_BASE_URL, '/signals', {
    method: 'POST',
    bearer: tenant.accessToken,
    body: {
      workspaceId: tenant.workspaceId,
      source: 'manual',
      kind: 'manual',
      payload: {
        title,
        body,
        sourceUrl: 'https://example.test/source-record',
      },
    },
  });
  expect(signal.id).toMatch(/^[0-9a-f-]{36}$/i);
  return signal.id;
}

async function addWorkspaceMember(owner: TestTenant, member: TestTenant): Promise<void> {
  const { response } = await requestJson<unknown>(
    AUTH_API_URL,
    `/workspaces/${owner.workspaceId}/members`,
    {
      method: 'POST',
      bearer: owner.accessToken,
      // This endpoint adds an existing account directly and sends no invite.
      body: { email: member.email, role: 'member' },
    },
  );
  expect(response.ok).toBe(true);
}

async function inviteProjectMember(
  owner: TestTenant,
  member: TestTenant,
  projectRole: 'editor' | 'viewer',
): Promise<void> {
  const { body: invite } = await requestJson<{ token: string }>(
    AUTH_API_URL,
    `/workspaces/${owner.workspaceId}/invites`,
    { method: 'POST', bearer: owner.accessToken, body: { role: 'member' } },
  );
  await requestJson<unknown>(AUTH_API_URL, `/invites/${invite.token}/accept`, {
    method: 'POST',
    bearer: member.accessToken,
  });
  await requestJson<unknown>(API_BASE_URL, `/projects/${owner.projectId}/members`, {
    method: 'POST',
    bearer: owner.accessToken,
    body: { userId: member.userId, role: projectRole },
  });
}

interface BrowserDiagnostics {
  consoleProblems: Array<{ type: string; text: string; target: string }>;
  pageErrors: string[];
  responsesAtLeast400: Array<{ method: string; status: number; target: string }>;
  expectedHttpConsoleFailures: ExpectedHttpConsoleFailure[];
}

interface ExpectedHttpConsoleFailure {
  method: string;
  status: 403 | 422;
  target: string;
}

function requestTarget(url: string): string {
  const parsed = new URL(url);
  return `${parsed.origin}${parsed.pathname}`;
}

function monitorBrowser(page: Page): BrowserDiagnostics {
  const diagnostics: BrowserDiagnostics = {
    consoleProblems: [],
    pageErrors: [],
    responsesAtLeast400: [],
    expectedHttpConsoleFailures: [],
  };
  page.on('console', (message) => {
    if (message.type() === 'error' || message.type() === 'warning') {
      const location = message.location();
      diagnostics.consoleProblems.push({
        type: message.type(),
        text: `${message.text()} at ${location.lineNumber}:${location.columnNumber}`,
        target: location.url ? requestTarget(location.url) : '',
      });
    }
  });
  page.on('response', (response) => {
    if (response.status() >= 400) {
      diagnostics.responsesAtLeast400.push({
        method: response.request().method(),
        status: response.status(),
        target: requestTarget(response.url()),
      });
    }
  });
  page.on('pageerror', (error) => diagnostics.pageErrors.push(error.message));
  return diagnostics;
}

async function attachBrowserDiagnostics(
  name: string,
  diagnostics: BrowserDiagnostics | undefined,
): Promise<void> {
  if (!diagnostics) return;
  await attachArtifact(
    `${name}-browser-diagnostics.json`,
    JSON.stringify(diagnostics, null, 2),
    'application/json',
  );
}

async function attachArtifact(name: string, body: string, contentType: string): Promise<void> {
  const path = test.info().outputPath(name);
  await writeFile(path, body);
  await test.info().attach(name, {
    path,
    contentType,
  });
}

function registerExpectedHttpConsoleFailure(
  diagnostics: BrowserDiagnostics,
  response: Response,
  expected: ExpectedHttpConsoleFailure,
): void {
  expect({
    method: response.request().method(),
    status: response.status(),
    target: requestTarget(response.url()),
  }).toEqual(expected);
  diagnostics.expectedHttpConsoleFailures.push(expected);
}

async function assertBrowserClean(diagnostics: BrowserDiagnostics): Promise<void> {
  const unexpectedResponses = [...diagnostics.responsesAtLeast400];
  const unexpectedConsoleProblems = [...diagnostics.consoleProblems];
  for (const expected of diagnostics.expectedHttpConsoleFailures) {
    const matchingResponses = diagnostics.responsesAtLeast400.filter(
      (entry) =>
        entry.method === expected.method &&
        entry.status === expected.status &&
        entry.target === expected.target,
    );
    expect(matchingResponses, 'the expected denied request should occur exactly once').toHaveLength(
      1,
    );
    const responseIndex = unexpectedResponses.findIndex(
      (entry) =>
        entry.method === expected.method &&
        entry.status === expected.status &&
        entry.target === expected.target,
    );
    if (responseIndex !== -1) unexpectedResponses.splice(responseIndex, 1);

    const matchesExpectedConsoleFailure = (entry: BrowserDiagnostics['consoleProblems'][number]) =>
      entry.type === 'error' &&
      entry.target === expected.target &&
      entry.text.includes(
        `status of ${expected.status} (${expected.status === 403 ? 'Forbidden' : 'Unprocessable Entity'})`,
      );
    await expect
      .poll(
        () => diagnostics.consoleProblems.filter(matchesExpectedConsoleFailure).length,
        'the expected HTTP failure should produce one matching browser console error',
      )
      .toBe(1);
    const consoleIndex = unexpectedConsoleProblems.findIndex(matchesExpectedConsoleFailure);
    if (consoleIndex !== -1) unexpectedConsoleProblems.splice(consoleIndex, 1);
  }
  expect(unexpectedResponses, 'unexpected HTTP error responses').toEqual([]);
  expect(unexpectedConsoleProblems, 'unexpected console errors or warnings').toEqual([]);
  expect(diagnostics.pageErrors, 'uncaught page errors').toEqual([]);
}

async function assertNoHorizontalOverflow(page: Page): Promise<void> {
  const hasOverflow = await page.evaluate(() => {
    const width = document.documentElement.clientWidth;
    return Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) > width + 1;
  });
  expect(hasOverflow, 'page should fit the viewport horizontally').toBe(false);
}

async function attachView(page: Page, name: string): Promise<void> {
  await test.info().attach(name, {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
}

async function newAuthenticatedPage(
  browser: Browser,
  tenant: TestTenant,
  viewport: { width: number; height: number },
): Promise<{ page: Page; close: () => Promise<void> }> {
  const context = await browser.newContext({ viewport });
  await injectAuth(context, tenant);
  return { page: await context.newPage(), close: () => context.close() };
}

async function runSourceCaptureScenario(
  browser: Browser,
  locale: Locale,
  viewport: { width: number; height: number },
  viewName: string,
): Promise<void> {
  const tenant = await createTestTenant({ locale });
  let closePage: (() => Promise<void>) | undefined;
  let diagnostics: BrowserDiagnostics | undefined;
  try {
    const sourceTitle = `Purchase request ${Date.now().toString(36)}`;
    const sourceBody = 'Replace the worn label printer before the next stock count.';
    const signalId = await createManualSignal(tenant, sourceTitle, sourceBody);
    const { page, close } = await newAuthenticatedPage(browser, tenant, viewport);
    closePage = close;
    diagnostics = monitorBrowser(page);
    const localized = copy[locale];

    await page.goto('/inbox');
    await expect(page.getByRole('heading', { name: localized.inbox.view.title })).toBeVisible();
    const inboxRow = page.locator('main ul > li').filter({ hasText: sourceTitle });
    await expect(inboxRow).toBeVisible({ timeout: 10_000 });
    await assertNoHorizontalOverflow(page);

    await inboxRow.getByRole('button', { name: localized.inbox.sourceIntake.review }).click();
    const dialog = page.getByRole('dialog');
    await expect(
      dialog.getByRole('heading', { name: localized.inbox.sourceIntake.title }),
    ).toBeVisible();
    await expect(dialog).toContainText(sourceBody);
    await attachView(page, `${viewName}-review-source`);

    const taskTitle = `${sourceTitle} · reviewed`;
    const taskBody = `${sourceBody}\nAcceptance: printer is ready for the next stock count.`;
    await dialog.getByLabel(localized.inbox.sourceIntake.task_title).fill(taskTitle);
    await dialog.getByLabel(localized.inbox.sourceIntake.task_body).fill(taskBody);
    await dialog.getByLabel(tenant.displayName).check();

    const dueOn = await page.evaluate(() => {
      const today = new Date();
      const month = String(today.getMonth() + 1).padStart(2, '0');
      const day = String(today.getDate()).padStart(2, '0');
      return `${today.getFullYear()}-${month}-${day}`;
    });
    await dialog.getByRole('button', { name: localized.common.common.date.placeholder }).click();
    await page.locator('[aria-current="date"]').click();
    await assertNoHorizontalOverflow(page);

    await dialog.getByRole('button', { name: localized.inbox.sourceIntake.create_task }).click();
    const openTask = dialog.getByRole('link', { name: localized.inbox.sourceIntake.open_task });
    await expect(openTask).toBeVisible({ timeout: 15_000 });
    await openTask.click();
    await expect(page).toHaveURL(/\/tasks\/[0-9a-f-]{36}$/i, { timeout: 10_000 });
    await expect(page.getByRole('heading', { level: 1, name: taskTitle })).toBeVisible();

    const sourceSection = page.getByRole('region', {
      name: localized.inbox.sourceIntake.task_sources,
    });
    await expect(sourceSection).toContainText(sourceTitle);
    await expect(sourceSection).toContainText(sourceBody);
    await assertNoHorizontalOverflow(page);
    await attachView(page, `${viewName}-task-with-source`);

    const taskId = new URL(page.url()).pathname.split('/').at(-1);
    if (!taskId) throw new Error('task route did not contain a public id');
    const task = await requestJson<{
      title: string;
      description: string;
      dueOn: string | null;
    }>(API_BASE_URL, `/tasks/${taskId}`, { method: 'GET', bearer: tenant.accessToken });
    expect(task.body.title).toBe(taskTitle);
    expect(task.body.description).toBe(taskBody);
    expect(task.body.dueOn).toBe(dueOn);

    const actors = await requestJson<{
      actors: Array<{ userId: string; role: string }>;
    }>(API_BASE_URL, `/tasks/${taskId}/actors`, {
      method: 'GET',
      bearer: tenant.accessToken,
    });
    expect(actors.body.actors).toContainEqual(
      expect.objectContaining({ userId: tenant.userId, role: 'assignee' }),
    );

    const sources = await requestJson<{
      sources: Array<{ intakeItemId: string; signalId: string; title: string; body: string }>;
    }>(API_BASE_URL, `/tasks/${taskId}/sources`, {
      method: 'GET',
      bearer: tenant.accessToken,
    });
    expect(sources.body.sources).toHaveLength(1);
    expect(sources.body.sources[0]).toMatchObject({
      signalId,
      title: sourceTitle,
      body: sourceBody,
    });

    // Replaying both real API writes models a retry after an uncertain
    // response. It must return the same intake row and task rather than fork.
    const intakeReplay = await requestJson<{
      intakeItemId: string;
      taskId?: string;
      created: boolean;
    }>(API_BASE_URL, `/workspaces/${tenant.workspaceId}/inbox/${signalId}/intake`, {
      method: 'POST',
      bearer: tenant.accessToken,
      body: {},
    });
    expect(intakeReplay.body.created).toBe(false);
    expect(intakeReplay.body.intakeItemId).toBe(sources.body.sources[0]?.intakeItemId);

    const conversionReplay = await requestJson<{ taskId: string }>(
      API_BASE_URL,
      `/workspaces/${tenant.workspaceId}/intake/${intakeReplay.body.intakeItemId}/convert`,
      {
        method: 'POST',
        bearer: tenant.accessToken,
        body: { projectId: tenant.projectId, title: 'Ignored on replay' },
      },
    );
    expect(conversionReplay.body.taskId).toBe(taskId);
    const sourcesAfterReplay = await requestJson<{
      sources: Array<{ intakeItemId: string }>;
    }>(API_BASE_URL, `/tasks/${taskId}/sources`, {
      method: 'GET',
      bearer: tenant.accessToken,
    });
    expect(sourcesAfterReplay.body.sources).toHaveLength(1);

    await page.reload();
    await expect(page.getByRole('heading', { level: 1, name: taskTitle })).toBeVisible();
    await expect(
      page.getByRole('region', { name: localized.inbox.sourceIntake.task_sources }),
    ).toContainText(sourceTitle);
    await assertBrowserClean(diagnostics);
  } finally {
    await attachBrowserDiagnostics(viewName, diagnostics);
    await closePage?.();
    await cleanupTenant(tenant);
  }
}

interface WorkflowSnapshot {
  workRevision: number;
  currentSubmission?: {
    id: string;
    revision: number;
    reviewerUserId: string;
    acceptanceCriteria: string[];
    decision?: { decision: string; reason?: string } | null;
  } | null;
  recentSubmissions: Array<{
    id: string;
    revision: number;
    decision?: { decision: string; reason?: string } | null;
  }>;
}

async function fetchWorkflow(tenant: TestTenant, taskId: string): Promise<WorkflowSnapshot> {
  const { body } = await requestJson<WorkflowSnapshot>(API_BASE_URL, `/tasks/${taskId}/workflow`, {
    method: 'GET',
    bearer: tenant.accessToken,
  });
  return body;
}

async function createReviewTask(
  author: TestTenant,
  reviewer: TestTenant,
  title: string,
): Promise<{ id: string }> {
  const task = await createTask(author, title);
  await requestJson<unknown>(API_BASE_URL, `/tasks/${task.id}/actors`, {
    method: 'POST',
    bearer: author.accessToken,
    body: { userId: reviewer.userId, role: 'reviewer' },
  });
  await requestJson<unknown>(API_BASE_URL, `/tasks/${task.id}/completion-policy`, {
    method: 'POST',
    bearer: author.accessToken,
    body: { policy: 'review_required' },
  });
  return { id: task.id };
}

async function submitWork(
  page: Page,
  localized: (typeof copy)['en'],
  criteria: string,
): Promise<void> {
  const submit = page.getByRole('button', { name: localized.common.taskWorkflow.submitWork });
  await page
    .getByRole('textbox', { name: localized.common.taskWorkflow.acceptanceCriteria })
    .fill(criteria);
  await page
    .getByLabel(localized.common.taskWorkflow.evidenceUrls)
    .fill('https://example.test/evidence/work-result');
  await expect(submit).toBeEnabled();
  await submit.click();
  await expect(
    currentSubmissionRegion(page, localized).getByText(
      localized.common.taskWorkflow.pendingReview,
      { exact: true },
    ),
  ).toBeVisible({
    timeout: 15_000,
  });
}

function currentSubmissionRegion(page: Page, localized: (typeof copy)['en']) {
  return page.getByRole('region', {
    name: localized.common.taskWorkflow.currentSubmission,
  });
}

test.describe('workflow improvements business paths', () => {
  test('manual source review creates one linked task with its reviewed details (desktop EN)', async ({
    browser,
  }) => {
    test.setTimeout(90_000);
    await runSourceCaptureScenario(browser, 'en', { width: 1280, height: 800 }, 'source-en');
  });

  test('manual source review fits mobile Japanese and preserves captured details', async ({
    browser,
  }) => {
    test.setTimeout(90_000);
    await runSourceCaptureScenario(browser, 'ja', { width: 375, height: 812 }, 'source-ja-mobile');
  });

  test('a separate reviewer can reject, see a revision resubmission, approve, and gate completion', async ({
    browser,
  }) => {
    test.setTimeout(150_000);
    const author = await createTestTenant({ locale: 'en' });
    const reviewer = await createTestTenant({ locale: 'en' });
    let authorClose: (() => Promise<void>) | undefined;
    let reviewerClose: (() => Promise<void>) | undefined;
    let authorDiagnostics: BrowserDiagnostics | undefined;
    let reviewerDiagnostics: BrowserDiagnostics | undefined;
    try {
      await inviteProjectMember(author, reviewer, 'viewer');
      const title = `Reviewable procedure ${Date.now().toString(36)}`;
      const task = await createReviewTask(author, reviewer, title);
      const authorBrowser = await newAuthenticatedPage(browser, author, {
        width: 1280,
        height: 800,
      });
      authorClose = authorBrowser.close;
      const reviewerBrowser = await newAuthenticatedPage(browser, reviewer, {
        width: 1280,
        height: 800,
      });
      reviewerClose = reviewerBrowser.close;
      const authorPage = authorBrowser.page;
      const reviewerPage = reviewerBrowser.page;
      authorDiagnostics = monitorBrowser(authorPage);
      reviewerDiagnostics = monitorBrowser(reviewerPage);
      const localized = copy.en;

      await authorPage.goto(`/tasks/${task.id}`);
      await expect(authorPage.getByRole('heading', { level: 1, name: title })).toBeVisible();
      await expect(
        authorPage.getByRole('heading', { name: localized.common.taskWorkflow.title }),
      ).toBeVisible();
      const submitForm = authorPage.getByRole('region', {
        name: localized.common.taskWorkflow.submitWork,
      });
      await expect(submitForm).toBeVisible();
      await submitWork(authorPage, localized, 'The steps reproduce the expected result.');
      await attachView(authorPage, 'review-author-pending');

      const firstWorkflow = await fetchWorkflow(author, task.id);
      const firstSubmission = firstWorkflow.currentSubmission;
      expect(firstSubmission?.reviewerUserId).toBe(reviewer.userId);
      expect(firstSubmission?.decision).toBeFalsy();
      expect(firstSubmission?.revision).toBe(firstWorkflow.workRevision);

      await reviewerPage.goto(`/tasks/${task.id}`);
      await expect(
        currentSubmissionRegion(reviewerPage, localized).getByText(
          localized.common.taskWorkflow.pendingReview,
          { exact: true },
        ),
      ).toBeVisible({
        timeout: 15_000,
      });
      await reviewerPage
        .getByRole('button', { name: localized.common.taskWorkflow.reject, exact: true })
        .click();
      const rejectionReason = 'Add one example showing the stock count was checked.';
      await reviewerPage
        .getByLabel(localized.common.taskWorkflow.rejectionReason)
        .fill(rejectionReason);
      const rejectResponse = reviewerPage.waitForResponse(
        (response) =>
          response.url().includes(`/tasks/${task.id}/submissions/`) &&
          response.url().endsWith('/decision') &&
          response.request().method() === 'POST',
      );
      await reviewerPage
        .getByRole('button', { name: localized.common.taskWorkflow.rejectSubmission })
        .click();
      expect((await rejectResponse).ok()).toBe(true);
      await expect(
        currentSubmissionRegion(reviewerPage, localized).getByText(
          localized.common.taskWorkflow.rejected,
          { exact: true },
        ),
      ).toBeVisible({ timeout: 15_000 });
      await expect(
        currentSubmissionRegion(reviewerPage, localized).getByText(rejectionReason, {
          exact: false,
        }),
      ).toBeVisible();

      const rejectedWorkflow = await fetchWorkflow(author, task.id);
      expect(rejectedWorkflow.currentSubmission?.decision).toMatchObject({
        decision: 'rejected',
        reason: rejectionReason,
      });

      // Rejection does not pass the completion gate. The task must remain
      // incomplete until a reviewer approves work on its current revision.
      const blockedComplete = authorPage.waitForResponse(
        (response) =>
          response.url().endsWith(`/tasks/${task.id}/transitions`) &&
          response.request().method() === 'POST',
      );
      await authorPage
        .getByRole('button', { name: localized.common.tasks.detail.transitions.complete })
        .click();
      const blockedCompleteResponse = await blockedComplete;
      expect(blockedCompleteResponse.status()).toBe(422);
      registerExpectedHttpConsoleFailure(authorDiagnostics, blockedCompleteResponse, {
        method: 'POST',
        status: 422,
        target: requestTarget(new URL(`/tasks/${task.id}/transitions`, API_BASE_URL).toString()),
      });
      const stillOpen = await requestJson<{ derivedState: string }>(
        API_BASE_URL,
        `/tasks/${task.id}`,
        { method: 'GET', bearer: author.accessToken },
      );
      expect(stillOpen.body.derivedState).not.toBe('done');

      await authorPage.reload();
      await expect(
        currentSubmissionRegion(authorPage, localized).getByText(rejectionReason, {
          exact: false,
        }),
      ).toBeVisible({ timeout: 15_000 });
      await authorPage
        .locator('summary')
        .filter({ hasText: localized.common.taskWorkflow.submissionHistory })
        .click();
      const history = authorPage.getByRole('region', {
        name: localized.common.taskWorkflow.submissionHistory,
      });
      await expect(history).toContainText(rejectionReason);
      const criteria = authorPage.getByRole('textbox', {
        name: localized.common.taskWorkflow.acceptanceCriteria,
      });
      await expect(criteria).toHaveValue('');
      const resubmit = authorPage.getByRole('button', {
        name: localized.common.taskWorkflow.resubmitWork,
      });
      await expect(resubmit).toBeDisabled();

      const editTitle = authorPage.getByRole('button', {
        name: new RegExp(`^${localized.common.tasks.detail.title_edit}`),
      });
      await editTitle.click();
      const titleInput = authorPage.getByRole('textbox', {
        name: localized.common.tasks.detail.title_edit,
      });
      const revisedTitle = `${title} — verified example added`;
      await titleInput.fill(revisedTitle);
      await titleInput.press('Enter');
      await expect(authorPage.getByRole('heading', { level: 1, name: revisedTitle })).toBeVisible();
      await submitWork(
        authorPage,
        localized,
        'The expected result is shown with the new stock-count example.',
      );

      const resubmittedWorkflow = await fetchWorkflow(author, task.id);
      const revisedSubmission = resubmittedWorkflow.currentSubmission;
      expect(revisedSubmission?.revision).toBeGreaterThan(firstSubmission?.revision ?? 0);
      expect(revisedSubmission?.decision).toBeFalsy();
      expect(
        resubmittedWorkflow.recentSubmissions.some(
          (entry) =>
            entry.id === firstSubmission?.id &&
            entry.decision?.decision === 'rejected' &&
            entry.decision.reason === rejectionReason,
        ),
      ).toBe(true);
      await attachView(authorPage, 'review-author-resubmitted-current-revision');

      await reviewerPage.reload();
      await expect(
        currentSubmissionRegion(reviewerPage, localized).getByText(
          localized.common.taskWorkflow.pendingReview,
          { exact: true },
        ),
      ).toBeVisible({ timeout: 15_000 });
      await reviewerPage
        .getByRole('button', { name: localized.common.taskWorkflow.approve, exact: true })
        .click();
      await expect(
        currentSubmissionRegion(reviewerPage, localized).getByText(
          localized.common.taskWorkflow.approved,
          { exact: true },
        ),
      ).toBeVisible({ timeout: 15_000 });
      const approvedWorkflow = await fetchWorkflow(author, task.id);
      expect(approvedWorkflow.currentSubmission?.decision?.decision).toBe('approved');
      expect(approvedWorkflow.currentSubmission?.revision).toBe(approvedWorkflow.workRevision);

      const completeResponse = authorPage.waitForResponse(
        (response) =>
          response.url().endsWith(`/tasks/${task.id}/transitions`) &&
          response.request().method() === 'POST',
      );
      await authorPage
        .getByRole('button', { name: localized.common.tasks.detail.transitions.complete })
        .click();
      expect((await completeResponse).ok()).toBe(true);
      const completed = await requestJson<{ derivedState: string }>(
        API_BASE_URL,
        `/tasks/${task.id}`,
        { method: 'GET', bearer: author.accessToken },
      );
      expect(completed.body.derivedState).toBe('done');

      // Exercise real access loss while a reviewer decision form is open.
      // The pending submission must stay pending and the UI must keep its
      // reason/action state when the API refuses the decision.
      const permissionTask = await createReviewTask(
        author,
        reviewer,
        `Second review ${Date.now().toString(36)}`,
      );
      await authorPage.goto(`/tasks/${permissionTask.id}`);
      await submitWork(authorPage, localized, 'Check the sample and confirm the result.');
      const pendingPermissionSubmission = (await fetchWorkflow(author, permissionTask.id))
        .currentSubmission;
      const pendingPermissionSubmissionId = pendingPermissionSubmission?.id;
      expect(pendingPermissionSubmissionId).toBeTruthy();
      if (!pendingPermissionSubmissionId) {
        throw new Error('permission test submission did not produce a current submission id');
      }
      const expectedDeniedDecision: ExpectedHttpConsoleFailure = {
        method: 'POST',
        status: 403,
        target: requestTarget(
          new URL(
            `/tasks/${permissionTask.id}/submissions/${pendingPermissionSubmissionId}/decision`,
            API_BASE_URL,
          ).toString(),
        ),
      };
      await reviewerPage.goto(`/tasks/${permissionTask.id}`);
      await expect(
        reviewerPage.getByRole('button', {
          name: localized.common.taskWorkflow.reject,
          exact: true,
        }),
      ).toBeVisible({ timeout: 15_000 });
      await reviewerPage
        .getByRole('button', { name: localized.common.taskWorkflow.reject, exact: true })
        .click();
      const permissionReason = 'This decision should not be accepted after membership is revoked.';
      await reviewerPage
        .getByLabel(localized.common.taskWorkflow.rejectionReason)
        .fill(permissionReason);
      await requestJson<{ ok: boolean }>(
        AUTH_API_URL,
        `/workspaces/${author.workspaceId}/members/${reviewer.userId}`,
        { method: 'DELETE', bearer: author.accessToken },
      );
      const deniedDecision = reviewerPage.waitForResponse(
        (response) =>
          response.url().includes(`/tasks/${permissionTask.id}/submissions/`) &&
          response.url().endsWith('/decision') &&
          response.request().method() === 'POST',
      );
      await reviewerPage
        .getByRole('button', { name: localized.common.taskWorkflow.rejectSubmission })
        .click();
      const deniedResponse = await deniedDecision;
      expect(deniedResponse.status()).toBe(403);
      registerExpectedHttpConsoleFailure(
        reviewerDiagnostics,
        deniedResponse,
        expectedDeniedDecision,
      );
      const decisionError = reviewerPage.getByRole('alert').last();
      await expect(decisionError).toBeVisible({ timeout: 10_000 });
      const decisionErrorText = ((await decisionError.textContent()) ?? '').trim();
      expect(decisionErrorText).not.toMatch(/\b(?:WS|AI|AUTH)\.[A-Z_.]+\b|\b[45]\d\d\b/);
      expect(
        Object.values(localized.errors).some((message) => message === decisionErrorText) ||
          decisionErrorText === localized.common.taskWorkflow.decisionFailed,
      ).toBe(true);
      await expect(
        currentSubmissionRegion(reviewerPage, localized).getByText(
          localized.common.taskWorkflow.pendingReview,
          { exact: true },
        ),
      ).toBeVisible();
      await expect(
        reviewerPage.getByLabel(localized.common.taskWorkflow.rejectionReason),
      ).toHaveValue(permissionReason);
      const stillPending = await fetchWorkflow(author, permissionTask.id);
      expect(stillPending.currentSubmission?.decision).toBeFalsy();
      await attachView(reviewerPage, 'review-permission-lost-pending');

      await assertBrowserClean(authorDiagnostics);
      await assertBrowserClean(reviewerDiagnostics);
    } finally {
      await attachBrowserDiagnostics('review-author', authorDiagnostics);
      await attachBrowserDiagnostics('review-reviewer', reviewerDiagnostics);
      await authorClose?.();
      await reviewerClose?.();
      await cleanupTenant(reviewer);
      await cleanupTenant(author);
    }
  });

  test('deterministic inbox triage keeps a failed Dock action pending and retries the real snooze', async ({
    browser,
  }) => {
    test.setTimeout(120_000);
    const owner = await createTestTenant({ locale: 'en' });
    const actor = await createTestTenant({ locale: 'en' });
    let closePage: (() => Promise<void>) | undefined;
    let diagnostics: BrowserDiagnostics | undefined;
    try {
      await inviteProjectMember(owner, actor, 'viewer');
      const sourceTitle = `Check supplier update ${Date.now().toString(36)}`;
      // Fresh signals from every currently registered API kind have the
      // deterministic fallback's default score (about 0.45), which recommends
      // snooze. Archive/open need a weighted kind outside the current signal
      // registry or an older server timestamp, so this API-only case does not
      // claim those actions.
      const signalId = await createManualSignal(
        owner,
        sourceTitle,
        'The supplier confirmed the revised delivery window.',
      );
      const browserPage = await newAuthenticatedPage(browser, actor, {
        width: 1280,
        height: 800,
      });
      closePage = browserPage.close;
      const page = browserPage.page;
      diagnostics = monitorBrowser(page);
      const localized = copy.en;

      await page.goto('/inbox');
      await page
        .locator('header')
        .getByLabel(localized.common.topbar.workspace.switcher)
        .selectOption(owner.workspaceId);
      await page
        .getByRole('main')
        .getByLabel(localized.inbox.filter.workspace_label)
        .selectOption(owner.workspaceId);
      const before = await requestJson<{
        items: Array<{ id: string; receivedAt: number }>;
      }>(API_BASE_URL, '/inbox', { method: 'GET', bearer: actor.accessToken });
      const sourceBefore = before.body.items.find((item) => item.id === signalId);
      expect(sourceBefore).toBeDefined();

      const triage = page.getByRole('button', { name: localized.ai.triage.trigger });
      await expect(triage).toBeEnabled();
      const triageRequest = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === `/workspaces/${owner.workspaceId}/inbox/triage` &&
          response.request().method() === 'POST',
      );
      await triage.click();
      const triageResponse = await triageRequest;
      const triageResponseText = await triageResponse.text();
      let triageResponseBody: unknown;
      try {
        triageResponseBody = JSON.parse(triageResponseText);
      } catch {
        triageResponseBody = triageResponseText.slice(0, 2_000);
      }
      const triageTarget = new URL(triageResponse.url()).pathname;
      await attachArtifact(
        'triage-http-response.json',
        JSON.stringify(
          {
            request: { method: triageResponse.request().method(), target: triageTarget },
            response: { status: triageResponse.status(), body: triageResponseBody },
          },
          null,
          2,
        ),
        'application/json',
      );
      expect(triageResponse.status(), `POST ${triageTarget} should return HTTP 200`).toBe(200);

      const persistedSuggestion = await requestJson<{
        suggestions: Array<{ proposalId: string; inboxItemId: string; recommendedAction: string }>;
      }>(API_BASE_URL, `/workspaces/${owner.workspaceId}/ai/suggestions`, {
        method: 'GET',
        bearer: actor.accessToken,
      });
      await attachArtifact(
        'persisted-triage-suggestions.json',
        JSON.stringify(
          {
            request: {
              method: 'GET',
              target: `/workspaces/${owner.workspaceId}/ai/suggestions`,
            },
            response: {
              status: persistedSuggestion.response.status,
              body: persistedSuggestion.body,
            },
          },
          null,
          2,
        ),
        'application/json',
      );
      expect(persistedSuggestion.body.suggestions).toHaveLength(1);
      expect(persistedSuggestion.body.suggestions[0]).toMatchObject({
        inboxItemId: signalId,
        recommendedAction: 'snooze',
      });
      await attachArtifact(
        'triage-response-dom.html',
        await page.locator('body').innerHTML(),
        'text/html',
      );

      const inlineSuggestions = page.getByRole('region', {
        name: localized.ai.triage.result_title,
      });
      await expect(inlineSuggestions.getByText('snooze', { exact: true })).toBeVisible({
        timeout: 20_000,
      });
      await expect(
        inlineSuggestions.getByText('snooze — manual from manual', { exact: true }),
      ).toBeVisible();
      await page.reload();
      await page.getByRole('button', { name: localized.ai.glass_dock.expand }).click();
      const dock = page.getByRole('region', { name: localized.ai.glass_dock.title });
      await expect(dock.getByText('snooze — manual from manual', { exact: true })).toBeVisible({
        timeout: 15_000,
      });
      await expect(dock.getByText('snooze', { exact: true })).toBeVisible();
      await attachView(page, 'triage-deterministic-dock-snooze');

      // Keep the browser session open while the owner revokes actual
      // workspace membership. The command must fail without removing the
      // suggestion or navigating away, then succeed with the same receipt
      // key after membership is restored.
      await requestJson<{ ok: boolean }>(
        AUTH_API_URL,
        `/workspaces/${owner.workspaceId}/members/${actor.userId}`,
        { method: 'DELETE', bearer: owner.accessToken },
      );
      const executeUrl = `/workspaces/${owner.workspaceId}/ai/suggestions/${signalId}/execute`;
      const failedCommand = page.waitForResponse(
        (response) => response.url().endsWith(executeUrl) && response.request().method() === 'POST',
      );
      await dock.getByRole('button', { name: localized.ai.action.apply, exact: true }).click();
      const failedResponse = await failedCommand;
      expect(failedResponse.status()).toBe(403);
      registerExpectedHttpConsoleFailure(diagnostics, failedResponse, {
        method: 'POST',
        status: 403,
        target: requestTarget(new URL(executeUrl, API_BASE_URL).toString()),
      });
      await expect(page).toHaveURL(/\/inbox$/);
      await expect(
        page.getByText(localized.errors['WS.WORKSPACE.ACCESS_DENIED'], { exact: true }),
      ).toBeVisible({ timeout: 10_000 });
      await expect(page.getByText(sourceTitle, { exact: true })).toBeVisible();
      await expect(dock.getByText('snooze — manual from manual', { exact: true })).toBeVisible();
      await expect(dock.getByText('snooze', { exact: true })).toBeVisible();
      await attachView(page, 'triage-dock-permission-error-pending');

      const afterDenied = await requestJson<{
        items: Array<{ id: string; receivedAt: number }>;
      }>(API_BASE_URL, '/inbox', { method: 'GET', bearer: owner.accessToken });
      expect(afterDenied.body.items.find((item) => item.id === signalId)?.receivedAt).toBe(
        sourceBefore?.receivedAt,
      );

      await addWorkspaceMember(owner, actor);
      const retryCommand = page.waitForResponse(
        (response) => response.url().endsWith(executeUrl) && response.request().method() === 'POST',
      );
      await dock.getByRole('button', { name: localized.ai.action.apply, exact: true }).click();
      const retryResponse = await retryCommand;
      expect(retryResponse.ok()).toBe(true);
      const firstKey = failedResponse.request().postDataJSON().operationKey as string;
      const retryKey = retryResponse.request().postDataJSON().operationKey as string;
      expect(firstKey).toBeTruthy();
      expect(retryKey).toBe(firstKey);
      const commandResult = (await retryResponse.json()) as {
        status: string;
        action: string;
        inboxItemId: string;
        snoozeUntil: number;
      };
      expect(commandResult).toMatchObject({
        status: 'executed',
        action: 'snooze',
        inboxItemId: signalId,
      });
      expect(commandResult.snoozeUntil).toBeGreaterThan(Math.floor(Date.now() / 1000) + 45 * 60);

      const pendingSuggestions = await requestJson<{
        suggestions: Array<{ proposalId: string }>;
      }>(API_BASE_URL, `/workspaces/${owner.workspaceId}/ai/suggestions`, {
        method: 'GET',
        bearer: actor.accessToken,
      });
      expect(pendingSuggestions.body.suggestions).toEqual([]);
      const afterSnooze = await requestJson<{
        items: Array<{ id: string; receivedAt: number }>;
      }>(API_BASE_URL, '/inbox', { method: 'GET', bearer: actor.accessToken });
      expect(afterSnooze.body.items.some((item) => item.id === signalId)).toBe(false);

      await page.reload();
      await expect(page.getByText(sourceTitle, { exact: true })).toHaveCount(0);
      await page.getByRole('button', { name: localized.ai.glass_dock.expand }).click();
      const reloadedDock = page.getByRole('region', { name: localized.ai.glass_dock.title });
      await expect(
        reloadedDock.getByText(localized.ai.glass_dock.empty, { exact: true }),
      ).toBeVisible({
        timeout: 15_000,
      });
      await assertNoHorizontalOverflow(page);
      await assertBrowserClean(diagnostics);
    } finally {
      await attachBrowserDiagnostics('inbox-triage', diagnostics);
      await closePage?.();
      await cleanupTenant(actor);
      await cleanupTenant(owner);
    }
  });
});
