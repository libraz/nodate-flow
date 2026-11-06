import { fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { API_URL, server, useMockApi } from '@tests/msw/server';
import { HttpResponse, http } from 'msw';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { authStore } from '../../auth/auth-store';
import TaskCreateDialog from '../task-create-dialog';

vi.mock('react-i18next', async () => {
  const actual = await vi.importActual<typeof import('react-i18next')>('react-i18next');
  return {
    ...actual,
    useTranslation: () => ({
      t: (key: string, options?: { returnObjects?: boolean }) => {
        if (options?.returnObjects && key === 'common.date.weekdays') {
          return ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
        }
        return key;
      },
      i18n: { resolvedLanguage: 'en' },
    }),
  };
});

useMockApi();

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

describe('<TaskCreateDialog> AI due date', () => {
  it('preserves the selected due date and assignee when applying AI-assisted creation', async () => {
    const user = userEvent.setup();
    let applyBody: Record<string, unknown> | undefined;
    server.use(
      http.post(`${API_URL}/workspaces/ws-1/tasks/propose-smart`, () =>
        HttpResponse.json({
          suggestedAssignees: [
            {
              userPublicId: 'user-1',
              displayName: 'Avery Example',
              confidence: 0.9,
              reason: 'Owns the release process.',
            },
          ],
          subtasks: [],
        }),
      ),
      http.post(`${API_URL}/workspaces/ws-1/tasks/apply-smart`, async ({ request }) => {
        applyBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ taskId: 'task-1', subtaskIds: [] });
      }),
    );
    renderWithProviders(
      <TaskCreateDialog projectId="project-1" workspaceId="ws-1" open={true} onClose={vi.fn()} />,
    );

    fireEvent.change(screen.getByRole('textbox', { name: 'tasks.form.title' }), {
      target: { value: 'Prepare release handoff' },
    });
    await user.click(screen.getByRole('button', { name: 'common.date.placeholder' }));
    await user.click(await screen.findByRole('button', { name: /^15$/ }));
    await user.click(screen.getByRole('button', { name: 'tasks.smart_create.assist_button' }));
    await user.click(await screen.findByRole('button', { name: 'tasks.smart_create.apply' }));

    await waitFor(() => expect(applyBody).toBeDefined());
    expect(applyBody).toEqual(
      expect.objectContaining({
        projectId: 'project-1',
        title: 'Prepare release handoff',
        assigneeUserIds: ['user-1'],
        dueOn: expect.stringMatching(/^\d{4}-\d{2}-15$/),
      }),
    );
  });
});
