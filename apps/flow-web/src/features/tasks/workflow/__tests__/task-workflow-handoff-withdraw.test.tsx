import type { components } from '@nodate-flow/sdk';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { API_URL, AUTH_API_URL, server, useMockApi } from '@tests/msw/server';
import { HttpResponse, http } from 'msw';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { authStore } from '../../../auth/auth-store';
import TaskWorkflowPanel from '../task-workflow-panel';

const mocks = vi.hoisted(() => ({ confirmAction: vi.fn() }));

vi.mock('../../../../lib/confirm-action', () => ({
  confirmAction: mocks.confirmAction,
}));

useMockApi();

type WorkflowSnapshot = components['schemas']['WorkflowSnapshot'];
type WorkflowHandoff = components['schemas']['WorkflowHandoff'];

const taskId = 'task-1';
const workspaceId = 'workspace-1';
const holderId = 'holder-1';
const recipientId = 'recipient-1';

function seedSession(userId: string): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setSession(`${header}.${payload}.`, {
    id: userId,
    email: 'holder@example.test',
    displayName: 'Holder',
    locale: 'en',
    timezone: 'UTC',
    country: 'US',
    themePreference: 'system',
    isInstanceAdmin: false,
    hasPassword: false,
  });
}

function pendingHandoff(overrides: Partial<WorkflowHandoff> = {}): WorkflowHandoff {
  return {
    id: 'handoff-1',
    role: 'assignee',
    status: 'pending',
    createdAt: 1_700_000_000,
    fromUserId: holderId,
    toUserId: recipientId,
    canDecide: false,
    canCancel: false,
    ...overrides,
  };
}

function snapshotWith(handoff: WorkflowHandoff): WorkflowSnapshot {
  return {
    completionPolicy: 'manual',
    workRevision: 1,
    requiredConstraintIds: [],
    handoffs: [handoff],
    recentSubmissions: [],
    capabilities: {
      canSetPolicy: false,
      canSubmit: false,
      canDecideCurrentSubmission: false,
      canRequestHandoffRoles: [],
    },
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
        derivedState: 'in_progress',
        priority: 2,
        updatedAt: 1_700_000_000,
        createdAt: 1_699_000_000,
      }),
    ),
    http.get(`${API_URL}/tasks/${taskId}/actors`, () => HttpResponse.json({ actors: [] })),
    http.get(`${AUTH_API_URL}/workspaces/${workspaceId}/members`, () =>
      HttpResponse.json({
        members: [
          { userId: holderId, displayName: 'Hana Holder', email: 'h@example.test', role: 'member' },
          {
            userId: recipientId,
            displayName: 'Ren Recipient',
            email: 'r@example.test',
            role: 'member',
          },
        ],
      }),
    ),
    http.get(`${API_URL}/tasks/${taskId}/attachments`, () =>
      HttpResponse.json({ attachments: [] }),
    ),
  );
}

function renderPanel() {
  return renderWithProviders(
    <TaskWorkflowPanel taskId={taskId} workspaceId={workspaceId} projectId="project-1" />,
  );
}

beforeEach(() => {
  mocks.confirmAction.mockReset().mockResolvedValue(true);
});

afterEach(() => {
  authStore.getState().clearSession();
});

describe('<TaskWorkflowPanel> handoff withdrawal', () => {
  it('lets the holder withdraw a pending handoff after confirming', async () => {
    seedSession(holderId);
    const user = userEvent.setup();
    let snapshot = snapshotWith(pendingHandoff({ canCancel: true }));
    installReadHandlers(() => snapshot);
    const decisionBodies: unknown[] = [];
    server.use(
      http.post(`${API_URL}/tasks/${taskId}/handoffs/handoff-1/decision`, async ({ request }) => {
        decisionBodies.push(await request.json());
        snapshot = snapshotWith(pendingHandoff({ status: 'cancelled', resolvedAt: 1_700_000_100 }));
        return HttpResponse.json({
          id: 'handoff-1',
          taskId,
          role: 'assignee',
          status: 'cancelled',
          createdAt: 1_700_000_000,
          resolvedAt: 1_700_000_100,
          toUserId: recipientId,
        });
      }),
    );

    renderPanel();
    await user.click(await screen.findByRole('button', { name: 'taskWorkflow.withdrawHandoff' }));

    expect(mocks.confirmAction).toHaveBeenCalledTimes(1);
    expect(mocks.confirmAction.mock.calls[0]?.[0]).toMatchObject({ tone: 'danger' });
    await waitFor(() => expect(decisionBodies).toEqual([{ decision: 'cancel' }]));
    expect(await screen.findByText('taskWorkflow.handoffStatus.cancelled')).toBeDefined();
    expect(screen.queryByRole('button', { name: 'taskWorkflow.withdrawHandoff' })).toBeNull();
  });

  it('sends nothing when the confirmation is dismissed', async () => {
    seedSession(holderId);
    mocks.confirmAction.mockResolvedValue(false);
    const user = userEvent.setup();
    installReadHandlers(() => snapshotWith(pendingHandoff({ canCancel: true })));
    let posted = false;
    server.use(
      http.post(`${API_URL}/tasks/${taskId}/handoffs/handoff-1/decision`, () => {
        posted = true;
        return HttpResponse.json({});
      }),
    );

    renderPanel();
    await user.click(await screen.findByRole('button', { name: 'taskWorkflow.withdrawHandoff' }));

    await waitFor(() => expect(mocks.confirmAction).toHaveBeenCalledTimes(1));
    expect(posted).toBe(false);
    expect(screen.getByText('taskWorkflow.handoffStatus.pending')).toBeDefined();
  });

  it('attaches a failed withdrawal to the handoff row and keeps it pending', async () => {
    seedSession(holderId);
    const user = userEvent.setup();
    installReadHandlers(() => snapshotWith(pendingHandoff({ canCancel: true })));
    server.use(
      http.post(`${API_URL}/tasks/${taskId}/handoffs/handoff-1/decision`, () =>
        HttpResponse.json({ code: 'WS_TASK_ACCESS_DENIED' }, { status: 403 }),
      ),
    );

    renderPanel();
    const withdraw = await screen.findByRole('button', { name: 'taskWorkflow.withdrawHandoff' });
    await user.click(withdraw);

    const alert = await screen.findByRole('alert');
    expect(alert.closest('li')).toBe(withdraw.closest('li'));
    expect(screen.getByText('taskWorkflow.handoffStatus.pending')).toBeDefined();
    expect(
      (screen.getByRole('button', { name: 'taskWorkflow.withdrawHandoff' }) as HTMLButtonElement)
        .disabled,
    ).toBe(false);
  });

  it('does not offer withdrawal when the read model withholds it', async () => {
    seedSession(recipientId);
    installReadHandlers(() => snapshotWith(pendingHandoff({ canDecide: true })));

    renderPanel();

    expect(await screen.findByRole('button', { name: 'taskWorkflow.acceptHandoff' })).toBeDefined();
    expect(screen.queryByRole('button', { name: 'taskWorkflow.withdrawHandoff' })).toBeNull();
  });

  it('offers withdrawal to a caller other than the holder when the read model allows it', async () => {
    seedSession('lead-1');
    installReadHandlers(() => snapshotWith(pendingHandoff({ canCancel: true })));

    renderPanel();

    expect(
      await screen.findByRole('button', { name: 'taskWorkflow.withdrawHandoff' }),
    ).toBeDefined();
    expect(screen.queryByRole('button', { name: 'taskWorkflow.acceptHandoff' })).toBeNull();
  });
});
