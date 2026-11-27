/**
 * TaskTransitionCard: legal rows per state, recommended and cancel rows,
 * and the locked-while-pending behaviour (spinner on the clicked row only,
 * no double submit, error toast).
 */

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ post: vi.fn(), show: vi.fn() }));

vi.mock('../../../lib/sdk', () => ({
  sdk: { POST: mocks.post },
  authSdk: { POST: mocks.post },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.show },
}));

import type { TaskDerivedState } from '../api';
import TaskTransitionCard from '../task-transition-card';

function renderCard(state: TaskDerivedState = 'open') {
  return renderWithProviders(<TaskTransitionCard taskId="t1" projectId="p1" state={state} />);
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function ok() {
  return {
    data: { id: 't1', projectId: 'p1', derivedState: 'waiting' },
    error: undefined,
    response: new Response(null, { status: 200 }),
  };
}

function rows(): HTMLElement[] {
  return screen.getAllByRole('button');
}

beforeEach(() => {
  mocks.post.mockReset();
  mocks.show.mockReset();
});

describe('<TaskTransitionCard> rows', () => {
  it('lists the transitions legal from the state, with the current state badge', () => {
    renderCard('open');
    expect(screen.getByText('tasks.status.open')).toBeDefined();
    expect(rows().map((row) => row.textContent)).toEqual([
      expect.stringContaining('tasks.detail.transitions.start'),
      expect.stringContaining('tasks.detail.transitions.block'),
      expect.stringContaining('tasks.detail.transitions.complete'),
      expect.stringContaining('tasks.detail.transitions.cancel'),
    ]);
  });

  it('offers only reopen from a done task', () => {
    renderCard('done');
    expect(rows()).toHaveLength(1);
    expect(rows()[0]?.textContent).toContain('tasks.detail.transitions.reopen');
  });

  it('shows where each transition lands', () => {
    renderCard('open');
    const start = rows()[0] as HTMLElement;
    expect(within(start).getByText('tasks.status.waiting')).toBeDefined();
  });
});

describe('<TaskTransitionCard> submitting', () => {
  it('posts the clicked transition for the task', async () => {
    mocks.post.mockResolvedValue(ok());
    renderCard('open');
    fireEvent.click(screen.getByRole('button', { name: /tasks\.detail\.transitions\.start/ }));

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledTimes(1);
    });
    expect(mocks.post.mock.calls[0]?.[0]).toBe('/tasks/{id}/transitions');
    expect(mocks.post.mock.calls[0]?.[1]).toMatchObject({
      params: { path: { id: 't1' } },
      body: { transition: 'start' },
    });
  });

  it('locks every row while pending, spins only the clicked one and ignores a second click', async () => {
    const gate = deferred<ReturnType<typeof ok>>();
    mocks.post.mockReturnValue(gate.promise);
    renderCard('open');
    const start = screen.getByRole('button', { name: /tasks\.detail\.transitions\.start/ });
    fireEvent.click(start);

    await waitFor(() => {
      expect(start.getAttribute('data-pending')).toBe('');
    });
    for (const row of rows()) {
      expect(row.getAttribute('aria-disabled')).toBe('true');
    }
    expect(rows()[0]?.parentElement?.getAttribute('aria-busy')).toBe('true');
    expect(screen.getAllByRole('status')).toHaveLength(1);
    expect(within(start).getByRole('status')).toBeDefined();

    fireEvent.click(screen.getByRole('button', { name: /tasks\.detail\.transitions\.complete/ }));
    fireEvent.click(start);
    expect(mocks.post).toHaveBeenCalledTimes(1);

    gate.resolve(ok());
    await waitFor(() => {
      expect(screen.queryByRole('status')).toBeNull();
    });
    expect(start.getAttribute('aria-disabled')).toBeNull();
  });

  it('toasts a warning through formatApiError when the transition is refused', async () => {
    mocks.post.mockResolvedValue({
      data: undefined,
      error: { title: 'Conflict', status: 409 },
      response: new Response(null, { status: 409 }),
    });
    renderCard('open');
    fireEvent.click(screen.getByRole('button', { name: /tasks\.detail\.transitions\.cancel/ }));

    await waitFor(() => {
      expect(mocks.show).toHaveBeenCalledTimes(1);
    });
    expect(mocks.show.mock.calls[0]?.[0]).toMatchObject({ tone: 'warning' });
  });
});
