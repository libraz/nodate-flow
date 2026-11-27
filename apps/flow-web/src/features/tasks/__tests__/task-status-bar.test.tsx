/**
 * TaskStatusBar: state badge, transition buttons per state, the recommended
 * primary action, cancel and event creation in the "more" menu, and the
 * locked-while-pending behaviour (spinner on the clicked control only, no
 * double submit, error toast).
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

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  useSubtasksQuery: () => ({ data: { total: 0, tasks: [] } }),
}));

import type { TaskDerivedState } from '../api';
import TaskStatusBar from '../task-status-bar';

function renderBar(state: TaskDerivedState = 'open') {
  return renderWithProviders(
    <TaskStatusBar taskId="t1" projectId="p1" workspaceId="w1" state={state} title="Task" />,
  );
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

/** The transition buttons, in DOM order, without the "more" menu trigger. */
function rows(): HTMLElement[] {
  return screen
    .getAllByRole('button')
    .filter((b) => b.getAttribute('aria-label') !== 'tasks.detail.transitions.more');
}

function openMenu(): void {
  fireEvent.click(screen.getByRole('button', { name: 'tasks.detail.transitions.more' }));
}

function mockNarrow(narrow: boolean): void {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: narrow && query.includes('max-width'),
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
}

beforeEach(() => {
  mockNarrow(false);
  mocks.post.mockReset();
  mocks.show.mockReset();
});

describe('<TaskStatusBar> actions', () => {
  it('shows the state badge and the legal transitions with the recommended one first', () => {
    renderBar('open');
    expect(screen.getByText('tasks.status.open')).toBeDefined();
    expect(rows().map((row) => row.textContent)).toEqual([
      expect.stringContaining('tasks.detail.transitions.complete'),
      expect.stringContaining('tasks.detail.transitions.start'),
      expect.stringContaining('tasks.detail.transitions.block'),
    ]);
  });

  it('offers only reopen from a done task', () => {
    renderBar('done');
    expect(rows()).toHaveLength(1);
    expect(rows()[0]?.textContent).toContain('tasks.detail.transitions.reopen');
  });

  it('puts where each transition lands in the button title', () => {
    renderBar('open');
    const start = screen.getByRole('button', { name: /tasks\.detail\.transitions\.start/ });
    expect(start.getAttribute('title')).toBe('→ tasks.status.waiting');
  });

  it('keeps cancel and event creation in the more menu', () => {
    renderBar('open');
    expect(screen.queryByRole('menuitem')).toBeNull();
    openMenu();
    expect(screen.getAllByRole('menuitem').map((item) => item.textContent)).toEqual([
      'tasks.actions.create_event.trigger',
      'tasks.detail.transitions.cancel',
    ]);
  });

  it('leaves cancel out of the menu when the state does not allow it', () => {
    renderBar('done');
    openMenu();
    expect(screen.getAllByRole('menuitem')).toHaveLength(1);
  });
});

describe('<TaskStatusBar> submitting', () => {
  it('posts the clicked transition for the task', async () => {
    mocks.post.mockResolvedValue(ok());
    renderBar('open');
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

  it('locks every control while pending, spins only the clicked one and ignores a second click', async () => {
    const gate = deferred<ReturnType<typeof ok>>();
    mocks.post.mockReturnValue(gate.promise);
    renderBar('open');
    const start = screen.getByRole('button', { name: /tasks\.detail\.transitions\.start/ });
    fireEvent.click(start);

    await waitFor(() => {
      expect(start.getAttribute('data-pending')).toBe('');
    });
    for (const row of rows()) {
      expect(row.getAttribute('aria-disabled')).toBe('true');
    }
    expect(
      screen
        .getByRole('button', { name: 'tasks.detail.transitions.more' })
        .getAttribute('aria-disabled'),
    ).toBe('true');
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
    renderBar('open');
    openMenu();
    fireEvent.click(screen.getByRole('menuitem', { name: /tasks\.detail\.transitions\.cancel/ }));

    await waitFor(() => {
      expect(mocks.show).toHaveBeenCalledTimes(1);
    });
    expect(mocks.show.mock.calls[0]?.[0]).toMatchObject({ tone: 'warning' });
  });
});

describe('<TaskStatusBar> on a narrow screen', () => {
  it('keeps only the recommended action in the strip and moves the rest into the menu', () => {
    mockNarrow(true);
    renderBar('open');
    expect(rows()).toHaveLength(1);
    expect(rows()[0]?.textContent).toContain('tasks.detail.transitions.complete');
    openMenu();
    const items = screen.getAllByRole('menuitem').map((el) => el.textContent);
    expect(items).toEqual([
      expect.stringContaining('tasks.detail.transitions.start'),
      expect.stringContaining('tasks.detail.transitions.block'),
      expect.stringContaining('tasks.actions.create_event.trigger'),
      expect.stringContaining('tasks.detail.transitions.cancel'),
    ]);
  });
});
