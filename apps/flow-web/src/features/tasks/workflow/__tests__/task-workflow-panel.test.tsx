import type { components } from '@nodate-flow/sdk';
import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { API_URL, AUTH_API_URL, server, useMockApi } from '@tests/msw/server';
import { HttpResponse, http } from 'msw';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { authStore } from '../../../auth/auth-store';
import { keysForEvent } from '../../../realtime/event-to-keys';
import TaskWorkflowPanel from '../task-workflow-panel';

useMockApi();

type WorkflowSnapshot = components['schemas']['WorkflowSnapshot'];

const taskId = 'task-1';
const workspaceId = 'workspace-1';
const reviewerId = 'reviewer-1';

function seedAuthToken(): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setAccessToken(`${header}.${payload}.`);
}

function makeSnapshot(overrides: Partial<WorkflowSnapshot> = {}): WorkflowSnapshot {
  return {
    completionPolicy: 'review_required',
    workRevision: 4,
    requiredConstraintIds: [],
    currentSubmission: {
      id: 'submission-1',
      revision: 4,
      createdAt: 1_700_000_000,
      reviewerUserId: reviewerId,
      acceptanceCriteria: ['All release checks pass'],
      evidenceUrls: ['https://ci.example.test/runs/42'],
      attachments: [],
    },
    handoffs: [],
    recentSubmissions: [],
    capabilities: {
      canSetPolicy: false,
      canSubmit: false,
      canDecideCurrentSubmission: true,
      canRequestHandoffRoles: [],
    },
    ...overrides,
  };
}

function installReadHandlers(getSnapshot: () => WorkflowSnapshot): void {
  server.use(
    http.get(`${API_URL}/tasks/${taskId}/workflow`, () => HttpResponse.json(getSnapshot())),
    http.get(`${API_URL}/tasks/${taskId}`, () =>
      HttpResponse.json({
        id: taskId,
        projectId: 'project-1',
        workspaceId,
        title: 'Prepare release',
        derivedState: 'review',
        priority: 2,
        updatedAt: 1_700_000_000,
        createdAt: 1_699_000_000,
      }),
    ),
    http.get(`${API_URL}/tasks/${taskId}/actors`, () =>
      HttpResponse.json({
        actors: [
          {
            id: 'actor-1',
            userId: reviewerId,
            displayName: 'Aki Reviewer',
            email: 'aki@example.test',
            role: 'reviewer',
            createdAt: 1_700_000_000,
          },
          {
            id: 'actor-2',
            userId: 'reviewer-2',
            displayName: 'Bea Reviewer',
            email: 'bea@example.test',
            role: 'approver',
            createdAt: 1_700_000_000,
          },
        ],
      }),
    ),
    http.get(`${AUTH_API_URL}/workspaces/${workspaceId}/members`, () =>
      HttpResponse.json({
        members: [
          {
            userId: reviewerId,
            displayName: 'Aki Reviewer',
            email: 'aki@example.test',
            role: 'member',
          },
          {
            userId: 'reviewer-2',
            displayName: 'Bea Reviewer',
            email: 'bea@example.test',
            role: 'member',
          },
        ],
      }),
    ),
    http.get(`${API_URL}/tasks/${taskId}/attachments`, () =>
      HttpResponse.json({ attachments: [] }),
    ),
    http.post(`${API_URL}/tasks/${taskId}/constraints/evaluate`, () =>
      HttpResponse.json({ outcomes: [] }),
    ),
  );
}

function renderPanel() {
  return renderWithProviders(
    <TaskWorkflowPanel taskId={taskId} workspaceId={workspaceId} projectId="project-1" />,
  );
}

beforeEach(seedAuthToken);

afterEach(() => {
  authStore.getState().clearSession();
});

describe('<TaskWorkflowPanel>', () => {
  it('reloads persisted reviewer, revision, evidence, and decision state from the API', async () => {
    installReadHandlers(() =>
      makeSnapshot({
        currentSubmission: {
          id: 'submission-1',
          revision: 4,
          createdAt: 1_700_000_000,
          reviewerUserId: reviewerId,
          acceptanceCriteria: ['All release checks pass'],
          evidenceUrls: ['https://ci.example.test/runs/42'],
          attachments: [],
          decision: {
            decision: 'rejected',
            reason: 'The smoke test is missing.',
            decidedAt: 1_700_000_100,
            decidedByUserId: reviewerId,
          },
        },
        capabilities: {
          canSetPolicy: false,
          canSubmit: true,
          canDecideCurrentSubmission: false,
          canRequestHandoffRoles: [],
        },
      }),
    );

    renderPanel();

    expect(await screen.findByText('taskWorkflow.reviewerNamed')).toBeDefined();
    expect(screen.getByText('All release checks pass')).toBeDefined();
    expect(screen.getByText('The smoke test is missing.')).toBeDefined();
    const evidence = screen.getByRole('link', { name: 'https://ci.example.test/runs/42' });
    expect(evidence.getAttribute('href')).toBe('https://ci.example.test/runs/42');
  });

  it('submits immutable criteria and evidence to the selected reviewer', async () => {
    const user = userEvent.setup();
    let snapshot = makeSnapshot({
      capabilities: {
        canSetPolicy: false,
        canSubmit: true,
        canDecideCurrentSubmission: false,
        canRequestHandoffRoles: [],
      },
    });
    delete snapshot.currentSubmission;
    installReadHandlers(() => snapshot);
    let submittedBody: unknown;
    server.use(
      http.post(`${API_URL}/tasks/${taskId}/submissions`, async ({ request }) => {
        submittedBody = await request.json();
        snapshot = makeSnapshot({
          currentSubmission: {
            id: 'submission-1',
            revision: 4,
            createdAt: 1_700_000_000,
            reviewerUserId: 'reviewer-2',
            acceptanceCriteria: ['All release checks pass'],
            evidenceUrls: ['https://ci.example.test/runs/42'],
            attachments: [],
          },
          capabilities: {
            canSetPolicy: false,
            canSubmit: false,
            canDecideCurrentSubmission: false,
            canRequestHandoffRoles: [],
          },
        });
        return HttpResponse.json({
          id: 'submission-1',
          reviewerUserId: 'reviewer-2',
          acceptanceCriteria: ['All release checks pass'],
          evidenceUrls: ['https://ci.example.test/runs/42'],
          attachments: [],
          createdAt: 1_700_000_100,
        });
      }),
    );

    renderPanel();

    const reviewer = await screen.findByRole('combobox', { name: 'taskWorkflow.reviewer' });
    await user.click(reviewer);
    await user.click(await screen.findByRole('option', { name: 'Bea Reviewer' }));
    await user.type(
      screen.getByRole('textbox', { name: 'taskWorkflow.acceptanceCriteria' }),
      'All release checks pass',
    );
    await user.type(
      screen.getByRole('textbox', { name: 'taskWorkflow.evidenceUrls' }),
      'https://ci.example.test/runs/42',
    );
    await user.click(screen.getByRole('button', { name: 'taskWorkflow.submitWork' }));

    await waitFor(() =>
      expect(submittedBody).toEqual({
        reviewerUserId: 'reviewer-2',
        acceptanceCriteria: ['All release checks pass'],
        evidenceUrls: ['https://ci.example.test/runs/42'],
        attachmentIds: [],
      }),
    );
    expect(await screen.findByText('taskWorkflow.pendingReview')).toBeDefined();
  });

  it('sends a selected-reviewer approval to the API and reloads the saved decision', async () => {
    const user = userEvent.setup();
    let snapshot = makeSnapshot();
    let decisionBody: unknown;
    installReadHandlers(() => snapshot);
    server.use(
      http.post(
        `${API_URL}/tasks/${taskId}/submissions/submission-1/decision`,
        async ({ request }) => {
          decisionBody = await request.json();
          snapshot = makeSnapshot({
            currentSubmission: {
              id: 'submission-1',
              revision: 4,
              createdAt: 1_700_000_000,
              reviewerUserId: reviewerId,
              acceptanceCriteria: ['All release checks pass'],
              evidenceUrls: ['https://ci.example.test/runs/42'],
              attachments: [],
              decision: {
                decision: 'approved',
                decidedAt: 1_700_000_100,
                decidedByUserId: reviewerId,
              },
            },
            capabilities: {
              canSetPolicy: false,
              canSubmit: false,
              canDecideCurrentSubmission: false,
              canRequestHandoffRoles: [],
            },
          });
          return HttpResponse.json({
            id: 'decision-1',
            submissionId: 'submission-1',
            decision: 'approved',
            decidedAt: 1_700_000_100,
          });
        },
      ),
    );

    renderPanel();
    await user.click(await screen.findByRole('button', { name: 'taskWorkflow.approve' }));

    await waitFor(() => expect(decisionBody).toEqual({ decision: 'approve' }));
    expect(await screen.findByText('taskWorkflow.approved')).toBeDefined();
  });

  it('keeps a pending review visible when the reviewer decision request fails', async () => {
    installReadHandlers(() => makeSnapshot());
    server.use(
      http.post(`${API_URL}/tasks/${taskId}/submissions/submission-1/decision`, () =>
        HttpResponse.json(
          { type: 'TASK.WORKFLOW.FORBIDDEN', title: 'Forbidden', status: 403 },
          { status: 403 },
        ),
      ),
    );

    renderPanel();

    const approve = await screen.findByRole('button', { name: 'taskWorkflow.approve' });
    await act(async () => approve.click());

    expect(await screen.findByRole('alert')).toBeDefined();
    expect(screen.getByRole('button', { name: 'taskWorkflow.approve' })).toBeDefined();
    expect(screen.getByText('taskWorkflow.pendingReview')).toBeDefined();
  });

  it('marks a prior approval stale after the work revision changes', async () => {
    installReadHandlers(() =>
      makeSnapshot({
        currentSubmission: {
          id: 'submission-1',
          revision: 3,
          createdAt: 1_700_000_000,
          reviewerUserId: reviewerId,
          acceptanceCriteria: ['All release checks pass'],
          evidenceUrls: [],
          attachments: [],
          decision: {
            decision: 'approved',
            decidedAt: 1_700_000_100,
            decidedByUserId: reviewerId,
          },
        },
      }),
    );

    renderPanel();

    expect(await screen.findByText('taskWorkflow.staleApproval')).toBeDefined();
    expect(screen.queryByText('taskWorkflow.approved')).toBeNull();
    expect(screen.queryByRole('button', { name: 'taskWorkflow.approve' })).toBeNull();
  });

  it('shows persisted submissions in a collapsed, read-only history', async () => {
    const user = userEvent.setup();
    const snapshot = makeSnapshot({
      workRevision: 5,
      recentSubmissions: [
        {
          id: 'submission-approved-old',
          revision: 3,
          createdAt: 1_700_000_000,
          reviewerUserId: reviewerId,
          acceptanceCriteria: ['Old approved criterion'],
          evidenceUrls: [],
          attachments: [],
          decision: {
            decision: 'approved',
            decidedAt: 1_700_000_100,
            decidedByUserId: reviewerId,
          },
        },
        {
          id: 'submission-rejected-old',
          revision: 4,
          createdAt: 1_700_000_200,
          reviewerUserId: reviewerId,
          acceptanceCriteria: ['Criterion from rejected submission'],
          evidenceUrls: ['https://ci.example.test/runs/old'],
          attachments: [],
          decision: {
            decision: 'rejected',
            reason: 'The previous run failed.',
            decidedAt: 1_700_000_300,
            decidedByUserId: reviewerId,
          },
        },
      ],
      capabilities: {
        canSetPolicy: false,
        canSubmit: false,
        canDecideCurrentSubmission: true,
        canRequestHandoffRoles: [],
      },
    });
    delete snapshot.currentSubmission;
    installReadHandlers(() => snapshot);

    renderPanel();

    const disclosure = await screen.findByText('taskWorkflow.submissionHistory');
    expect(disclosure.closest('details')?.hasAttribute('open')).toBe(false);
    await user.click(disclosure);

    expect(disclosure.closest('details')?.hasAttribute('open')).toBe(true);
    expect(await screen.findByText('Old approved criterion')).toBeDefined();
    expect(screen.getAllByText('taskWorkflow.historyRevision')).toHaveLength(2);
    expect(document.querySelector('time[datetime="2023-11-14T22:13:20.000Z"]')).not.toBeNull();
    expect(screen.getByText('The previous run failed.')).toBeDefined();
    expect(
      screen.getByRole('link', { name: 'https://ci.example.test/runs/old' }).getAttribute('href'),
    ).toBe('https://ci.example.test/runs/old');
    expect(screen.getByText('taskWorkflow.staleHistoryApproval')).toBeDefined();
    expect(screen.queryByRole('button', { name: 'taskWorkflow.approve' })).toBeNull();
  });

  it('starts resubmission criteria blank and explains the start-to-review transition', async () => {
    installReadHandlers(() =>
      makeSnapshot({
        currentSubmission: {
          id: 'submission-rejected',
          revision: 4,
          createdAt: 1_700_000_000,
          reviewerUserId: reviewerId,
          acceptanceCriteria: ['Do not copy this old criterion'],
          evidenceUrls: [],
          attachments: [],
          decision: {
            decision: 'rejected',
            reason: 'Please include the smoke test.',
            decidedAt: 1_700_000_100,
            decidedByUserId: reviewerId,
          },
        },
        capabilities: {
          canSetPolicy: false,
          canSubmit: true,
          canDecideCurrentSubmission: false,
          canRequestHandoffRoles: [],
        },
      }),
    );

    renderPanel();

    expect(await screen.findByText('taskWorkflow.submitStartsReview')).toBeDefined();
    expect(screen.getByRole('textbox', { name: 'taskWorkflow.acceptanceCriteria' })).toHaveProperty(
      'value',
      '',
    );
    expect(screen.queryByText('Do not copy this old criterion')).toBeDefined();
  });

  it('keeps resubmission blocked when the server capability is false', async () => {
    installReadHandlers(() =>
      makeSnapshot({
        currentSubmission: {
          id: 'submission-rejected',
          revision: 4,
          createdAt: 1_700_000_000,
          reviewerUserId: reviewerId,
          acceptanceCriteria: ['Update the smoke test'],
          evidenceUrls: [],
          attachments: [],
          decision: {
            decision: 'rejected',
            reason: 'Please update the smoke test.',
            decidedAt: 1_700_000_100,
            decidedByUserId: reviewerId,
          },
        },
        capabilities: {
          canSetPolicy: false,
          canSubmit: false,
          canDecideCurrentSubmission: false,
          canRequestHandoffRoles: [],
        },
      }),
    );

    renderPanel();

    expect(await screen.findByText('Please update the smoke test.')).toBeDefined();
    expect(screen.queryByRole('textbox', { name: 'taskWorkflow.acceptanceCriteria' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'taskWorkflow.resubmitWork' })).toBeNull();
  });

  it('renders evidence URLs carrying credentials as plain text', async () => {
    const credentialUrl = 'https://user:secret@ci.example.test/runs/42';
    installReadHandlers(() =>
      makeSnapshot({
        currentSubmission: {
          id: 'submission-1',
          revision: 4,
          createdAt: 1_700_000_000,
          reviewerUserId: reviewerId,
          acceptanceCriteria: ['All release checks pass'],
          evidenceUrls: [credentialUrl],
          attachments: [],
        },
      }),
    );

    renderPanel();

    expect(await screen.findByText(credentialUrl)).toBeDefined();
    expect(screen.queryByRole('link', { name: credentialUrl })).toBeNull();
  });

  it('sends the handoff deadline as the end of the chosen day in the profile zone', async () => {
    const user = userEvent.setup();
    authStore.getState().setSession(authStore.getState().accessToken ?? '', {
      id: 'requester-1',
      email: 'requester@example.test',
      displayName: 'Requester',
      locale: 'en',
      timezone: 'Asia/Tokyo',
      country: 'JP',
      themePreference: 'system',
      isInstanceAdmin: false,
      hasPassword: false,
    });
    const snapshot = makeSnapshot({
      capabilities: {
        canSetPolicy: false,
        canSubmit: false,
        canDecideCurrentSubmission: false,
        canRequestHandoffRoles: ['assignee'],
      },
    });
    delete snapshot.currentSubmission;
    installReadHandlers(() => snapshot);
    let handoffBody: { dueAt?: number } | undefined;
    server.use(
      http.post(`${API_URL}/tasks/${taskId}/handoffs`, async ({ request }) => {
        handoffBody = (await request.json()) as { dueAt?: number };
        return HttpResponse.json({
          id: 'handoff-1',
          taskId,
          role: 'assignee',
          status: 'pending',
          createdAt: 1_700_000_000,
          toUserId: 'reviewer-2',
        });
      }),
    );

    renderPanel();

    await user.click(await screen.findByRole('combobox', { name: 'taskWorkflow.recipient' }));
    await user.click(await screen.findByRole('option', { name: 'Bea Reviewer' }));
    await user.click(screen.getByRole('button', { name: 'common.date.placeholder' }));
    await user.click(await screen.findByRole('button', { name: /^15$/ }));
    await user.click(screen.getByRole('button', { name: 'taskWorkflow.requestHandoff' }));

    await waitFor(() => expect(handoffBody?.dueAt).toBeTypeOf('number'));
    // Midnight after the 15th in Tokyo is 15:00 UTC on the 15th.
    expect(new Date(((handoffBody?.dueAt ?? 0) + 1) * 1000).toISOString()).toMatch(
      /-15T15:00:00\.000Z$/,
    );
  });

  it('labels an expired handoff as not accepted and offers no recipient decision', async () => {
    installReadHandlers(() =>
      makeSnapshot({
        handoffs: [
          {
            id: 'handoff-1',
            role: 'assignee',
            status: 'expired',
            createdAt: 1_700_000_000,
            dueAt: 1_700_000_100,
            toUserId: reviewerId,
            canDecide: false,
            canCancel: false,
          },
        ],
        capabilities: {
          canSetPolicy: false,
          canSubmit: false,
          canDecideCurrentSubmission: false,
          canRequestHandoffRoles: [],
        },
      }),
    );

    renderPanel();

    expect(await screen.findByText(/taskWorkflow\.handoffStatus\.expired/)).toBeDefined();
    expect(screen.queryByRole('button', { name: 'taskWorkflow.acceptHandoff' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'taskWorkflow.declineHandoff' })).toBeNull();
  });

  it('accepts a pending handoff only when the recipient capability is present', async () => {
    const user = userEvent.setup();
    let snapshot = makeSnapshot({
      handoffs: [
        {
          id: 'handoff-1',
          role: 'assignee',
          status: 'pending',
          createdAt: 1_700_000_000,
          toUserId: reviewerId,
          canDecide: true,
          canCancel: false,
        },
      ],
      capabilities: {
        canSetPolicy: false,
        canSubmit: false,
        canDecideCurrentSubmission: false,
        canRequestHandoffRoles: [],
      },
    });
    delete snapshot.currentSubmission;
    installReadHandlers(() => snapshot);
    let decisionBody: unknown;
    server.use(
      http.post(`${API_URL}/tasks/${taskId}/handoffs/handoff-1/decision`, async ({ request }) => {
        decisionBody = await request.json();
        snapshot = {
          ...snapshot,
          handoffs: [
            {
              id: 'handoff-1',
              role: 'assignee',
              status: 'accepted',
              createdAt: 1_700_000_000,
              resolvedAt: 1_700_000_100,
              toUserId: reviewerId,
              canDecide: false,
              canCancel: false,
            },
          ],
        };
        return HttpResponse.json({
          id: 'handoff-1',
          taskId,
          role: 'assignee',
          status: 'accepted',
          createdAt: 1_700_000_000,
          resolvedAt: 1_700_000_100,
          toUserId: reviewerId,
        });
      }),
    );

    renderPanel();

    await user.click(await screen.findByRole('button', { name: 'taskWorkflow.acceptHandoff' }));

    await waitFor(() => expect(decisionBody).toEqual({ decision: 'accept' }));
    expect(await screen.findByText('taskWorkflow.handoffStatus.accepted')).toBeDefined();
    expect(screen.queryByRole('button', { name: 'taskWorkflow.acceptHandoff' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'taskWorkflow.declineHandoff' })).toBeNull();
  });

  it('refetches the second decision and revision through task.changed invalidation', async () => {
    let snapshot = makeSnapshot({
      currentSubmission: {
        id: 'submission-1',
        revision: 4,
        createdAt: 1_700_000_000,
        reviewerUserId: reviewerId,
        acceptanceCriteria: ['All release checks pass'],
        evidenceUrls: ['https://ci.example.test/runs/42'],
        attachments: [],
        decision: {
          decision: 'approved',
          decidedAt: 1_700_000_100,
          decidedByUserId: reviewerId,
        },
      },
      capabilities: {
        canSetPolicy: false,
        canSubmit: true,
        canDecideCurrentSubmission: false,
        canRequestHandoffRoles: [],
      },
    });
    installReadHandlers(() => snapshot);
    const view = renderPanel();

    expect(await screen.findByText('taskWorkflow.approved')).toBeDefined();
    snapshot = makeSnapshot({
      workRevision: 5,
      currentSubmission: {
        id: 'submission-2',
        revision: 5,
        createdAt: 1_700_000_200,
        reviewerUserId: reviewerId,
        acceptanceCriteria: ['Smoke test passes'],
        evidenceUrls: ['https://ci.example.test/runs/43'],
        attachments: [],
        decision: {
          decision: 'rejected',
          reason: 'The smoke test still fails.',
          decidedAt: 1_700_000_200,
          decidedByUserId: reviewerId,
        },
      },
      capabilities: {
        canSetPolicy: false,
        canSubmit: true,
        canDecideCurrentSubmission: false,
        canRequestHandoffRoles: [],
      },
    });
    const prefixes = keysForEvent({ kind: 'task.changed', workspaceId, at: 1_700_000_200 });
    await act(async () => {
      await Promise.all(
        prefixes.map((queryKey) => view.queryClient.invalidateQueries({ queryKey })),
      );
    });

    expect(await screen.findByText('taskWorkflow.rejected')).toBeDefined();
    expect(screen.getByText('The smoke test still fails.')).toBeDefined();
    expect(screen.getByText('taskWorkflow.revision')).toBeDefined();
  });
});
