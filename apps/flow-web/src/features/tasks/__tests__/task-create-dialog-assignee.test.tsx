/**
 * TaskCreateDialog assignee field: defaults to the caller, sends `actors`
 * only for someone else (by user id, not member id), always lands the field
 * value in an AI-assisted create, and never blocks the form on the member
 * list.
 */

import { fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  create: vi.fn(),
  apply: vi.fn(),
  members: vi.fn(),
}));

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    useCreateTask: () => ({ mutateAsync: mocks.create, isPending: false }),
  };
});

vi.mock('../smart-create-api', () => ({
  useProposeSmartTask: () => ({
    mutate: (_args: unknown, options: { onSuccess: (data: unknown) => void }) => {
      options.onSuccess({
        suggestedAssignees: [
          { userPublicId: 'user-carol', displayName: 'Carol', confidence: 0.9, reason: 'fit' },
        ],
        subtasks: [],
      });
    },
    isPending: false,
  }),
  useApplySmartTask: () => ({ mutateAsync: mocks.apply, isPending: false }),
}));

vi.mock('../../workspaces/api', async () => {
  const actual =
    await vi.importActual<typeof import('../../workspaces/api')>('../../workspaces/api');
  return { ...actual, useWorkspaceMembersQuery: () => mocks.members() };
});

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

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

import { type AuthUser, authStore } from '../../auth/auth-store';
import TaskCreateDialog from '../task-create-dialog';

const MEMBERS = [
  { id: 'member-self', userId: 'user-self', displayName: 'Sam', joinedAt: 1 },
  { id: 'member-bob', userId: 'user-bob', displayName: 'Bob', joinedAt: 1 },
  { id: 'member-pending', userId: 'user-pending', displayName: 'Pia', invitedAt: 1 },
];

function renderDialog() {
  return renderWithProviders(
    <TaskCreateDialog projectId="proj-1" workspaceId="ws-1" open={true} onClose={vi.fn()} />,
  );
}

function typeTitle(value: string): void {
  fireEvent.change(screen.getByRole('textbox', { name: /tasks\.form\.title/ }), {
    target: { value },
  });
}

function submit(): void {
  const button = screen.getByRole('button', { name: /tasks\.form\.submit/ });
  fireEvent.submit(button.closest('form') as HTMLFormElement);
}

beforeEach(() => {
  mocks.create.mockReset().mockResolvedValue({ id: 'task-1' });
  mocks.apply.mockReset().mockResolvedValue({ taskId: 'task-1', subtaskIds: [] });
  mocks.members.mockReset().mockReturnValue({ data: MEMBERS });
  authStore.getState().setSession('token', {
    id: 'user-self',
    displayName: 'Sam',
  } as unknown as AuthUser);
});

describe('<TaskCreateDialog> assignee field', () => {
  it('starts on the caller and omits actors', async () => {
    renderDialog();

    const field = await screen.findByRole('combobox', { name: 'tasks.form.assignee' });
    expect((field as HTMLInputElement).value).toBe('tasks.form.assignee_self');

    typeTitle('Plan the launch');
    submit();

    await waitFor(() => {
      expect(mocks.create).toHaveBeenCalledTimes(1);
    });
    expect(mocks.create.mock.calls[0]?.[0]).not.toHaveProperty('actors');
  });

  it('sends the chosen member by user id, and only joined members are offered', async () => {
    const user = userEvent.setup();
    renderDialog();

    const field = await screen.findByRole('combobox', { name: 'tasks.form.assignee' });
    await user.click(field);
    expect(await screen.findByRole('option', { name: 'Bob' })).toBeTruthy();
    expect(screen.queryByRole('option', { name: 'Pia' })).toBeNull();
    await user.click(screen.getByRole('option', { name: 'Bob' }));

    typeTitle('Plan the launch');
    submit();

    await waitFor(() => {
      expect(mocks.create).toHaveBeenCalledTimes(1);
    });
    expect(mocks.create.mock.calls[0]?.[0]).toMatchObject({
      actors: [{ role: 'assignee', userId: 'user-bob' }],
    });
  });

  it('always includes the field value, the caller here, in an AI apply', async () => {
    renderDialog();
    await screen.findByRole('combobox', { name: 'tasks.form.assignee' });

    typeTitle('Plan the launch');
    fireEvent.click(screen.getByRole('button', { name: /tasks\.smart_create\.assist_button/ }));
    fireEvent.click(await screen.findByRole('button', { name: /tasks\.smart_create\.apply/ }));

    await waitFor(() => {
      expect(mocks.apply).toHaveBeenCalledTimes(1);
    });
    const ids =
      (mocks.apply.mock.calls[0]?.[0] as { assigneeUserIds: string[] } | undefined)
        ?.assigneeUserIds ?? [];
    expect([...ids].sort()).toEqual(['user-carol', 'user-self']);
  });

  it('lets the title be typed and submitted while the member list is still loading', async () => {
    mocks.members.mockImplementation(() => {
      throw new Promise(() => {});
    });
    renderDialog();

    typeTitle('Plan the launch');
    expect(
      (screen.getByRole('textbox', { name: /tasks\.form\.title/ }) as HTMLInputElement).value,
    ).toBe('Plan the launch');
    submit();

    await waitFor(() => {
      expect(mocks.create).toHaveBeenCalledTimes(1);
    });
    expect(mocks.create.mock.calls[0]?.[0]).not.toHaveProperty('actors');
  });
});
