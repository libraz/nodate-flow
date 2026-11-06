import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { API_URL, AUTH_API_URL, server, useMockApi } from '@tests/msw/server';
import { HttpResponse, http } from 'msw';
import { Suspense } from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { authStore } from '../../../auth/auth-store';
import TaskRoleAssignments from '../task-role-assignments';
import TaskWorkflowPanel from '../task-workflow-panel';

useMockApi();

const taskId = 'task-1';
const workspaceId = 'workspace-1';

function seedAuthToken(): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setAccessToken(`${header}.${payload}.`);
}

beforeEach(seedAuthToken);

afterEach(() => {
  authStore.getState().clearSession();
});

describe('<TaskRoleAssignments>', () => {
  it('keeps roles read-only while a reviewer can still decide the submission', async () => {
    const actors = [
      {
        id: 'actor-1',
        userId: 'user-1',
        displayName: 'Sam Reviewer',
        email: 'sam@example.test',
        role: 'reviewer',
        createdAt: 1_700_000_000,
      },
    ];
    server.use(
      http.get(`${API_URL}/tasks/${taskId}/actors`, () => HttpResponse.json({ actors })),
      http.get(`${AUTH_API_URL}/workspaces/${workspaceId}/members`, () =>
        HttpResponse.json({
          members: [
            {
              userId: 'user-1',
              displayName: 'Sam Reviewer',
              email: 'sam@example.test',
              role: 'member',
            },
          ],
        }),
      ),
      http.get(`${API_URL}/tasks/${taskId}/workflow`, () =>
        HttpResponse.json({
          completionPolicy: 'review_required',
          workRevision: 1,
          requiredConstraintIds: [],
          currentSubmission: {
            id: 'submission-1',
            revision: 1,
            createdAt: 1_700_000_000,
            reviewerUserId: 'user-1',
            acceptanceCriteria: ['The feature is ready'],
            evidenceUrls: [],
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
        }),
      ),
      http.get(`${API_URL}/tasks/${taskId}`, () =>
        HttpResponse.json({
          id: taskId,
          projectId: 'project-1',
          workspaceId,
          title: 'Review task',
          derivedState: 'review',
          priority: 2,
          createdAt: 1_700_000_000,
          updatedAt: 1_700_000_000,
        }),
      ),
      http.get(`${API_URL}/tasks/${taskId}/attachments`, () =>
        HttpResponse.json({ attachments: [] }),
      ),
    );

    renderWithProviders(
      <Suspense fallback={<p>loading</p>}>
        <TaskRoleAssignments taskId={taskId} workspaceId={workspaceId} />
        <TaskWorkflowPanel taskId={taskId} workspaceId={workspaceId} projectId="project-1" />
      </Suspense>,
    );

    expect(await screen.findByText('Sam Reviewer')).toBeDefined();
    expect(await screen.findByRole('button', { name: 'taskWorkflow.approve' })).toBeDefined();
    expect(screen.queryByRole('button', { name: 'taskWorkflow.addReviewer' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'taskWorkflow.addApprover' })).toBeNull();
    expect(screen.queryByRole('button', { name: /taskWorkflow\.removeRole/ })).toBeNull();
  });

  it('assigns a human reviewer through the existing task actor API', async () => {
    const user = userEvent.setup();
    let actors: Array<Record<string, unknown>> = [];
    let requestBody: unknown;
    server.use(
      http.get(`${API_URL}/tasks/${taskId}/actors`, () => HttpResponse.json({ actors })),
      http.get(`${AUTH_API_URL}/workspaces/${workspaceId}/members`, () =>
        HttpResponse.json({
          members: [
            {
              userId: 'user-1',
              displayName: 'Sam Reviewer',
              email: 'sam@example.test',
              role: 'member',
            },
          ],
        }),
      ),
      http.get(`${API_URL}/tasks/${taskId}/workflow`, () =>
        HttpResponse.json({
          completionPolicy: 'manual',
          workRevision: 1,
          requiredConstraintIds: [],
          handoffs: [],
          recentSubmissions: [],
          capabilities: {
            canSetPolicy: true,
            canSubmit: false,
            canDecideCurrentSubmission: false,
            canRequestHandoffRoles: [],
          },
        }),
      ),
      http.post(`${API_URL}/tasks/${taskId}/actors`, async ({ request }) => {
        requestBody = await request.json();
        actors = [
          {
            id: 'actor-1',
            userId: 'user-1',
            displayName: 'Sam Reviewer',
            email: 'sam@example.test',
            role: 'reviewer',
            createdAt: 1_700_000_000,
          },
        ];
        return HttpResponse.json(actors[0]);
      }),
    );

    renderWithProviders(
      <Suspense fallback={<p>loading</p>}>
        <TaskRoleAssignments taskId={taskId} workspaceId={workspaceId} />
      </Suspense>,
    );

    await user.click(await screen.findByRole('button', { name: 'taskWorkflow.addReviewer' }));
    const picker = await screen.findByRole('combobox', { name: 'taskWorkflow.addReviewer' });
    await user.click(picker);
    await user.click(await screen.findByRole('option', { name: 'Sam Reviewer' }));

    await waitFor(() => expect(requestBody).toEqual({ role: 'reviewer', userId: 'user-1' }));
    expect(await screen.findByText('Sam Reviewer')).toBeDefined();
    expect(screen.getByText('taskWorkflow.reviewerRole')).toBeDefined();
  });
});
