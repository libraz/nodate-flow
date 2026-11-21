import { QueryClient } from '@tanstack/react-query';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { API_URL, server, useMockApi } from '@tests/msw/server';
import { HttpResponse, http } from 'msw';
import { Suspense } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { authStore } from '../../../auth/auth-store';
import { type Project, projectsKeys } from '../../../projects/api';
import { type WorkspaceMember, workspacesKeys } from '../../../workspaces/api';
import type { IntakeItem } from '../../intake/api';
import IntakeConvertDialog from '../../intake/intake-convert-dialog';

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

vi.mock('@nodate-flow/ui/primitives/toast', () => ({ toaster: { show: vi.fn() } }));

useMockApi();

const item = {
  id: 'intake-1',
  title: 'Review the release handoff',
  body: 'Please check the release notes before publishing.',
  sourceUrl: 'https://github.com/acme/service/pull/17',
  triageStatus: 'pending',
  createdAt: 1_700_000_000,
} as IntakeItem;

function seedAuthToken(): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setAccessToken(`${header}.${payload}.`);
}

function renderDialog() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Number.POSITIVE_INFINITY } },
  });
  queryClient.setQueryData(projectsKeys.list('ws-1'), [
    { id: 'project-1', name: 'Platform', identifier: 'PLAT' } as Project,
    { id: 'project-old', name: 'Retired', identifier: 'OLD', isArchived: true } as Project,
  ]);
  queryClient.setQueryData(workspacesKeys.members('ws-1'), [
    {
      id: 'member-1',
      userId: 'user-1',
      displayName: 'Avery Example',
      email: 'avery@example.test',
      role: 'member',
    } as WorkspaceMember,
  ]);
  const onClose = vi.fn();
  renderWithProviders(
    <Suspense fallback={<p>loading</p>}>
      <IntakeConvertDialog workspaceId="ws-1" item={item} open={true} onClose={onClose} />
    </Suspense>,
    { queryClient },
  );
  return { onClose };
}

beforeEach(seedAuthToken);

afterEach(() => {
  authStore.getState().clearSession();
});

describe('IntakeConvertDialog source preservation', () => {
  it('sends the editable source title and body, selected assignee, and due date on confirmation', async () => {
    const user = userEvent.setup();
    let convertBody: Record<string, unknown> | undefined;
    server.use(
      http.post(`${API_URL}/workspaces/ws-1/intake/intake-1/convert`, async ({ request }) => {
        convertBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          { code: 'INTERNAL.UNEXPECTED', message: 'Temporary failure' },
          { status: 500 },
        );
      }),
    );
    renderDialog();

    expect(screen.queryByText(/Retired/)).toBeNull();
    expect(screen.getByDisplayValue(item.title)).toBeDefined();
    expect(screen.getByDisplayValue(item.body ?? '')).toBeDefined();
    expect(
      screen.getByRole('link', { name: 'sourceIntake.open_source' }).getAttribute('href'),
    ).toBe(item.sourceUrl);
    await user.click(screen.getByRole('checkbox', { name: 'Avery Example' }));
    await user.click(screen.getByRole('button', { name: 'common.date.placeholder' }));
    await user.click(await screen.findByRole('button', { name: /^15$/ }));
    fireEvent.change(screen.getByRole('textbox', { name: 'sourceIntake.task_title' }), {
      target: { value: 'Edited release handoff' },
    });
    await user.click(screen.getByRole('button', { name: 'intake.convert.submit' }));

    await waitFor(() => expect(convertBody).toBeDefined());
    expect(convertBody).toEqual({
      projectId: 'project-1',
      title: 'Edited release handoff',
      body: item.body,
      assigneeUserIds: ['user-1'],
      dueOn: expect.stringMatching(/^\d{4}-\d{2}-15$/),
    });
    expect(screen.getByDisplayValue('Edited release handoff')).toBeDefined();
  });
});
