/**
 * CompleteTaskButton: which states can be completed in place, how undo
 * restores the starting state, and that the outcome toasts survive the
 * row unmounting under the click.
 */

import { fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
  show: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: { POST: mocks.post },
  authSdk: { POST: mocks.post },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.show },
}));

import { type MyTaskListItem, myTasksKey, type TaskDerivedState } from '../api';
import CompleteTaskButton from '../complete-task-button';

const MY_TASKS_KEY = myTasksKey({ sort: 'dueOn' });

function task(derivedState: TaskDerivedState) {
  return { id: 't1', title: 'Write report', derivedState, projectId: 'p1' };
}

function ok(derivedState: TaskDerivedState = 'done') {
  return Promise.resolve({
    data: { id: 't1', projectId: 'p1', derivedState },
    error: undefined,
    response: new Response(null, { status: 200 }),
  });
}

function refused() {
  return Promise.resolve({
    data: undefined,
    error: { title: 'Internal Server Error', status: 500 },
    response: new Response(null, { status: 400 }),
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function transitions(): string[] {
  return mocks.post.mock.calls.map(
    (call) => (call[1] as { body: { transition: string } }).body.transition,
  );
}

function row(derivedState: TaskDerivedState): MyTaskListItem {
  return {
    id: 't1',
    title: 'Write report',
    derivedState,
    priority: 0,
    projectId: 'p1',
    workspaceId: 'ws-1',
    workspaceName: 'WS',
    actorRole: 'assignee',
    createdAt: 1,
  };
}

beforeEach(() => {
  mocks.post.mockReset();
  mocks.show.mockReset();
});

describe('<CompleteTaskButton> availability', () => {
  it.each(['open', 'review'] as const)('completes a %s task with one POST', async (state) => {
    mocks.post.mockImplementation(() => ok());
    renderWithProviders(<CompleteTaskButton task={task(state)} />);

    const button = screen.getByRole('button', { name: 'today.complete_task' });
    expect(button.getAttribute('aria-disabled')).toBeNull();
    fireEvent.click(button);

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledTimes(1);
    });
    expect(mocks.post.mock.calls[0]?.[0]).toBe('/tasks/{id}/transitions');
    expect(transitions()).toEqual(['complete']);
  });

  it('enables the button on a waiting task and completes it', async () => {
    mocks.post.mockImplementation(() => ok());
    renderWithProviders(<CompleteTaskButton task={task('waiting')} />);

    const button = screen.getByRole('button', { name: 'today.complete_task' });
    expect(button.getAttribute('aria-disabled')).toBeNull();
    fireEvent.click(button);

    await waitFor(() => {
      expect(transitions()).toEqual(['complete']);
    });
  });

  it.each(['done', 'cancelled'] as const)('does nothing when a %s task is clicked', (state) => {
    renderWithProviders(<CompleteTaskButton task={task(state)} />);

    fireEvent.click(screen.getByRole('button', { name: 'today.complete_task' }));
    expect(mocks.post).not.toHaveBeenCalled();
  });

  it('carries no reason or description on the button', () => {
    renderWithProviders(<CompleteTaskButton task={task('open')} />);
    const button = screen.getByRole('button', { name: 'today.complete_task' });
    expect(button.getAttribute('aria-describedby')).toBeNull();
    expect(button.getAttribute('title')).toBeNull();
  });

  it('is aria-disabled while the request is pending and ignores a second click', async () => {
    const gate = deferred<unknown>();
    mocks.post.mockImplementation(() => gate.promise);
    renderWithProviders(<CompleteTaskButton task={task('open')} />);

    const button = screen.getByRole('button', { name: 'today.complete_task' });
    fireEvent.click(button);
    await waitFor(() => {
      expect(button.getAttribute('aria-disabled')).toBe('true');
    });
    fireEvent.click(button);

    gate.resolve(await ok());
    await waitFor(() => {
      expect(mocks.show).toHaveBeenCalledTimes(1);
    });
    expect(mocks.post).toHaveBeenCalledTimes(1);
  });
});

describe('<CompleteTaskButton> toasts', () => {
  it('shows the success toast with an undo action after the row has unmounted', async () => {
    const gate = deferred<unknown>();
    mocks.post.mockImplementation(() => gate.promise);
    const { unmount } = renderWithProviders(<CompleteTaskButton task={task('open')} />);

    fireEvent.click(screen.getByRole('button', { name: 'today.complete_task' }));
    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledTimes(1);
    });
    unmount();
    gate.resolve(await ok());

    await waitFor(() => {
      expect(mocks.show).toHaveBeenCalledTimes(1);
    });
    const toast = mocks.show.mock.calls[0]?.[0] as {
      tone: string;
      message: string;
      action: { label: string };
    };
    expect(toast.tone).toBe('success');
    expect(toast.message).toBe('today.completed_toast');
    expect(toast.action.label).toBe('common.undo');
  });

  it('shows the error toast through formatApiError after the row has unmounted', async () => {
    const gate = deferred<unknown>();
    mocks.post.mockImplementation(() => gate.promise);
    const { unmount } = renderWithProviders(<CompleteTaskButton task={task('open')} />);

    fireEvent.click(screen.getByRole('button', { name: 'today.complete_task' }));
    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledTimes(1);
    });
    unmount();
    gate.resolve(await refused());

    await waitFor(() => {
      expect(mocks.show).toHaveBeenCalledTimes(1);
    });
    expect(mocks.show.mock.calls[0]?.[0]).toMatchObject({
      tone: 'danger',
      message: 'today.complete_error',
    });
  });
});

describe('<CompleteTaskButton> undo', () => {
  async function completeThenUndo(
    from: TaskDerivedState,
    gateLast?: Promise<unknown>,
  ): Promise<ReturnType<typeof renderWithProviders>> {
    const view = renderWithProviders(<CompleteTaskButton task={task(from)} />);
    view.queryClient.setQueryData(MY_TASKS_KEY, [row(from)]);
    mocks.post.mockImplementation(() => ok());
    fireEvent.click(screen.getByRole('button', { name: 'today.complete_task' }));
    await waitFor(() => {
      expect(mocks.show).toHaveBeenCalledTimes(1);
    });
    if (gateLast) {
      mocks.post.mockImplementationOnce(() => ok('waiting'));
      mocks.post.mockImplementationOnce(() => gateLast);
    }
    const toast = mocks.show.mock.calls[0]?.[0] as { action: { onAction: () => void } };
    toast.action.onAction();
    return view;
  }

  it('walks reopen then unblock for a task that was open', async () => {
    await completeThenUndo('open');
    await waitFor(() => {
      expect(transitions()).toEqual(['complete', 'reopen', 'unblock']);
    });
    expect(mocks.show).toHaveBeenCalledTimes(1);
  });

  it('issues exactly one reopen for a task that was waiting', async () => {
    await completeThenUndo('waiting');
    await waitFor(() => {
      expect(transitions()).toEqual(['complete', 'reopen']);
    });
    expect(mocks.show).toHaveBeenCalledTimes(1);
  });

  it('walks reopen then submit for a task that was in review', async () => {
    await completeThenUndo('review');
    await waitFor(() => {
      expect(transitions()).toEqual(['complete', 'reopen', 'submit']);
    });
    expect(mocks.show).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['open', 'open'],
    ['review', 'review'],
  ] as const)(
    'puts the optimistic badge of a %s task back on the last step',
    async (from, back) => {
      const gate = deferred<unknown>();
      const view = await completeThenUndo(from, gate.promise);
      await waitFor(() => {
        expect(transitions()).toHaveLength(3);
      });
      const rows = view.queryClient.getQueryData<MyTaskListItem[]>(MY_TASKS_KEY);
      expect(rows?.[0]?.derivedState).toBe(back);
      gate.resolve(await ok(back));
    },
  );

  it('reports a failed second step with the undo error toast', async () => {
    const view = renderWithProviders(<CompleteTaskButton task={task('open')} />);
    mocks.post
      .mockImplementationOnce(() => ok())
      .mockImplementationOnce(() => ok('waiting'))
      .mockImplementationOnce(() => refused());
    fireEvent.click(screen.getByRole('button', { name: 'today.complete_task' }));
    await waitFor(() => {
      expect(mocks.show).toHaveBeenCalledTimes(1);
    });
    view.unmount();

    const toast = mocks.show.mock.calls[0]?.[0] as { action: { onAction: () => void } };
    toast.action.onAction();

    await waitFor(() => {
      expect(mocks.show).toHaveBeenCalledTimes(2);
    });
    expect(mocks.show.mock.calls[1]?.[0]).toMatchObject({
      tone: 'danger',
      message: 'today.undo_error',
    });
  });
});
