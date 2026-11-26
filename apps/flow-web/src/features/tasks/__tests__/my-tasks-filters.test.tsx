/**
 * /today filters and sort: the query sent to `GET /me/tasks`, the list
 * layout per sort, empty states and the pending (deferred) indication.
 */

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ get: vi.fn(), authGet: vi.fn() }));

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: mocks.get, POST: vi.fn() },
  authSdk: { GET: mocks.authGet, POST: vi.fn() },
}));

vi.mock('../../../components/layout/glass-dock', () => ({
  OPEN_CREATE_TASK_EVENT: 'nf:open-create-task',
  OPEN_COMMAND_PALETTE_EVENT: 'nf:open-command-palette',
}));

vi.mock('../../../components/page-header', () => ({
  default: ({ title }: { title: string }) => <h1>{title}</h1>,
}));

vi.mock('../../../lib/use-effective-timezone', async () => {
  const { Zone } = await import('@nodate-flow/ui/time');
  return { useEffectiveZone: () => Zone.resolve('UTC', 'UTC') };
});

import { Route } from '../../../routes/_authenticated.today';
import type { MyTaskListItem } from '../api';
import { resetMyTaskFilters } from '../use-my-task-filters';

const TodayRoute = Route.options.component as () => ReactElement;

const DEFAULT_STATES = ['open', 'waiting', 'review'];

function row(overrides: Partial<MyTaskListItem> & { id: string; title: string }): MyTaskListItem {
  return {
    derivedState: 'open',
    priority: 0,
    projectId: 'p1',
    workspaceId: 'ws-1',
    workspaceName: 'Workspace',
    actorRole: 'assignee',
    createdAt: 1,
    ...overrides,
  };
}

function ok(data: unknown): Promise<unknown> {
  return Promise.resolve({ data, error: undefined, response: new Response(null, { status: 200 }) });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function serve(rows: MyTaskListItem[] | ((query: Record<string, unknown>) => MyTaskListItem[])) {
  mocks.get.mockImplementation(
    (_path: string, init: { params: { query: Record<string, unknown> } }) =>
      ok({ tasks: typeof rows === 'function' ? rows(init.params.query) : rows, nextCursor: null }),
  );
}

function queries(): Record<string, unknown>[] {
  return mocks.get.mock.calls.map(
    (call) => (call[1] as { params: { query: Record<string, unknown> } }).params.query,
  );
}

function lastQuery(): Record<string, unknown> | undefined {
  return queries().at(-1);
}

const COPY: Record<string, string> = {
  'today.filters.button_label': '{count, plural, =0 {Filters} other {Filters, # active}}',
  'tasks.filters.chip_remove': 'Remove filter: {label}',
  'tasks.filters.chip_status': 'Status: {value}',
  'tasks.filters.chip_priority': 'Priority: {value}',
  'today.filters.chip_role': 'Role: {value}',
  'today.filters.chip_workspace': 'Workspace: {value}',
};

function Seed(): null {
  const { i18n } = useTranslation('common');
  for (const [key, value] of Object.entries(COPY)) i18n.addResource('en', 'common', key, value);
  return null;
}

function renderToday() {
  return renderWithProviders(
    <Suspense fallback={null}>
      <Seed />
      <TodayRoute />
    </Suspense>,
  );
}

const FILTERS_BUTTON = /^Filters/;

/** The Filters trigger; its accessible name carries the active-group count. */
function filtersButton(): HTMLElement {
  return screen.getByRole('button', { name: FILTERS_BUTTON });
}

/** Open the Filters popover (once) and return one of its groups. */
function filterGroup(name: string): HTMLElement {
  if (!screen.queryByRole('dialog')) fireEvent.click(filtersButton());
  return within(screen.getByRole('dialog')).getByRole('group', { name });
}

async function renderLoaded(title?: string) {
  const view = renderToday();
  if (title) await screen.findByText(title);
  else await waitFor(() => expect(mocks.get).toHaveBeenCalled());
  return view;
}

function titlesInOrder(): string[] {
  return screen.getAllByRole('link').map((a) => a.textContent ?? '');
}

beforeEach(() => {
  mocks.get.mockReset();
  mocks.authGet.mockReset();
  mocks.authGet.mockImplementation(() =>
    ok({
      workspaces: [
        { id: 'ws-1', name: 'Alpha' },
        { id: 'ws-2', name: 'Beta' },
      ],
    }),
  );
  window.localStorage.clear();
  resetMyTaskFilters();
});

afterEach(() => {
  window.localStorage.clear();
});

describe('/today filters — query', () => {
  it('sends the default states and sort, and nothing else', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    const query = lastQuery();
    expect(query).toMatchObject({ limit: 200, offset: 0, state: DEFAULT_STATES, sort: 'dueOn' });
    for (const key of ['q', 'priority', 'role', 'workspaceId']) {
      expect(query).not.toHaveProperty(key);
    }
  });

  it('refetches with the state chosen in the status group', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    const group = filterGroup('tasks.filters.status');
    fireEvent.click(within(group).getByRole('button', { name: 'tasks.status.done' }));

    await waitFor(() => {
      expect(lastQuery()).toMatchObject({ state: [...DEFAULT_STATES, 'done'] });
    });
  });

  it('shows one status chip listing the selection, and dismissing it restores the default', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    const view = renderToday();
    await screen.findByText('Alpha task');

    const group = filterGroup('tasks.filters.status');
    fireEvent.click(within(group).getByRole('button', { name: 'tasks.status.done' }));
    fireEvent.click(within(group).getByRole('button', { name: 'tasks.status.waiting' }));
    await waitFor(() => {
      expect(lastQuery()).toMatchObject({ state: ['open', 'review', 'done'] });
    });

    const status = screen.getByRole('status');
    const chips = within(status).getAllByText(/^Status:/);
    expect(chips).toHaveLength(1);
    expect(chips[0]?.textContent).toBe(
      new Intl.ListFormat('en', { type: 'conjunction' })
        .format(['tasks.status.open', 'tasks.status.review', 'tasks.status.done'])
        .replace(/^/, 'Status: '),
    );

    fireEvent.click(
      within(status).getByRole('button', {
        name: `Remove filter: ${chips[0]?.textContent ?? ''}`,
      }),
    );
    // The default result is already cached, so no new request is made; the
    // restored selection shows in the toggles and the chip is gone.
    await waitFor(() => {
      expect(screen.queryByText(/^Status:/)).toBeNull();
    });
    const pressed = within(group)
      .getAllByRole('button')
      .filter((b) => b.getAttribute('aria-pressed') === 'true')
      .map((b) => b.textContent);
    expect(pressed).toEqual(['tasks.status.open', 'tasks.status.waiting', 'tasks.status.review']);
    view.unmount();
  });

  it('refetches with the priority chosen', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    const group = filterGroup('tasks.filters.priority');
    fireEvent.click(within(group).getByRole('button', { name: 'tasks.priority.urgent' }));

    await waitFor(() => {
      expect(lastQuery()).toMatchObject({ priority: [4] });
    });
  });

  it('refetches with the role chosen', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    const group = filterGroup('today.filters.role');
    fireEvent.click(within(group).getByRole('button', { name: 'today.filters.roles.reviewer' }));

    await waitFor(() => {
      expect(lastQuery()).toMatchObject({ role: ['reviewer'] });
    });
  });

  it('refetches with the workspace chosen', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    fireEvent.click(filtersButton());
    const select = await screen.findByRole('combobox', { name: 'today.filters.workspace' });
    fireEvent.change(select, { target: { value: 'ws-2' } });

    await waitFor(() => {
      expect(lastQuery()).toMatchObject({ workspaceId: 'ws-2' });
    });
  });

  it('hides the workspace select for a single workspace', async () => {
    mocks.authGet.mockImplementation(() => ok({ workspaces: [{ id: 'ws-1', name: 'Alpha' }] }));
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    filterGroup('tasks.filters.status');
    await waitFor(() => expect(mocks.authGet).toHaveBeenCalled());
    expect(screen.queryByRole('combobox', { name: 'today.filters.workspace' })).toBeNull();
  });

  it('shows no chips and no count under the defaults', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    expect(filtersButton().getAttribute('aria-label')).toBe('Filters');
    expect(screen.queryByRole('status')?.textContent ?? '').toBe('');
  });

  it('counts active groups, not values, and leaves search out', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    fireEvent.change(screen.getByRole('searchbox', { name: 'tasks.filters.search_placeholder' }), {
      target: { value: 'report' },
    });
    expect(filtersButton().getAttribute('aria-label')).toBe('Filters');

    fireEvent.click(
      within(filterGroup('tasks.filters.priority')).getByRole('button', {
        name: 'tasks.priority.urgent',
      }),
    );
    fireEvent.click(
      within(filterGroup('tasks.filters.priority')).getByRole('button', {
        name: 'tasks.priority.high',
      }),
    );
    await waitFor(() => {
      expect(filtersButton().getAttribute('aria-label')).toBe('Filters, 1 active');
    });
    fireEvent.click(
      within(filterGroup('today.filters.role')).getByRole('button', {
        name: 'today.filters.roles.reviewer',
      }),
    );
    await waitFor(() => {
      expect(filtersButton().getAttribute('aria-label')).toBe('Filters, 2 active');
    });
    // The popover stays open while toggling.
    expect(screen.getByRole('dialog')).toBeTruthy();
  });

  it('shows one chip per group with joined values and a translated remove label', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    const priority = filterGroup('tasks.filters.priority');
    fireEvent.click(within(priority).getByRole('button', { name: 'tasks.priority.urgent' }));
    fireEvent.click(within(priority).getByRole('button', { name: 'tasks.priority.high' }));
    const roles = filterGroup('today.filters.role');
    fireEvent.click(within(roles).getByRole('button', { name: 'today.filters.roles.reviewer' }));

    const status = screen.getByRole('status');
    const list = new Intl.ListFormat('en', { type: 'conjunction' });
    const priorityText = `Priority: ${list.format(['tasks.priority.high', 'tasks.priority.urgent'])}`;
    await waitFor(() => {
      expect(within(status).getByText(priorityText)).toBeTruthy();
    });
    expect(within(status).getAllByText(/^Priority:/)).toHaveLength(1);
    expect(within(status).getAllByText(/^Role:/)).toHaveLength(1);
    expect(
      within(status).getByRole('button', { name: `Remove filter: ${priorityText}` }),
    ).toBeTruthy();
  });

  it('dismissing a chip resets its whole group', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    const priority = filterGroup('tasks.filters.priority');
    fireEvent.click(within(priority).getByRole('button', { name: 'tasks.priority.urgent' }));
    fireEvent.click(within(priority).getByRole('button', { name: 'tasks.priority.high' }));
    await waitFor(() => {
      expect(lastQuery()).toMatchObject({ priority: [3, 4] });
    });

    const status = screen.getByRole('status');
    fireEvent.click(within(status).getByRole('button', { name: /^Remove filter: Priority:/ }));

    // The default result is already cached, so the proof is the chip and count going away.
    await waitFor(() => {
      expect(within(status).queryByText(/^Priority:/)).toBeNull();
    });
    expect(filtersButton().getAttribute('aria-label')).toBe('Filters');
    const pressed = within(filterGroup('tasks.filters.priority'))
      .getAllByRole('button')
      .filter((b) => b.getAttribute('aria-pressed') === 'true');
    expect(pressed).toHaveLength(0);
  });

  it('shows a workspace chip once a workspace is chosen', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    fireEvent.click(filtersButton());
    const select = await screen.findByRole('combobox', { name: 'today.filters.workspace' });
    fireEvent.change(select, { target: { value: 'ws-2' } });

    const chip = await within(screen.getByRole('status')).findByRole('button', {
      name: 'Remove filter: Workspace: Beta',
    });
    expect(chip).toBeTruthy();
  });

  it('refetches with the trimmed search text', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    fireEvent.change(screen.getByRole('searchbox', { name: 'tasks.filters.search_placeholder' }), {
      target: { value: '  report ' },
    });

    await waitFor(() => {
      expect(lastQuery()).toMatchObject({ q: 'report' });
    });
  });

  it('refetches with the sort chosen', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    fireEvent.change(screen.getByRole('combobox', { name: 'today.sort.label' }), {
      target: { value: '-priority' },
    });

    await waitFor(() => {
      expect(lastQuery()).toMatchObject({ sort: '-priority' });
    });
    expect(window.localStorage.getItem('nf:my-tasks-sort')).toBe('-priority');
  });

  it('clear restores the default query and keeps the sort', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');
    expect(screen.queryByRole('status')?.textContent ?? '').toBe('');

    const group = filterGroup('tasks.filters.priority');
    fireEvent.click(within(group).getByRole('button', { name: 'tasks.priority.high' }));
    await waitFor(() => {
      expect(lastQuery()).toMatchObject({ priority: [3] });
    });
    fireEvent.change(screen.getByRole('combobox', { name: 'today.sort.label' }), {
      target: { value: 'title' },
    });
    await waitFor(() => {
      expect(lastQuery()).toMatchObject({ priority: [3], sort: 'title' });
    });

    const [clearAll] = within(screen.getByRole('status')).getAllByRole('button', {
      name: 'tasks.filters.clear',
    });
    fireEvent.click(clearAll as HTMLElement);

    await waitFor(() => {
      expect(lastQuery()).not.toHaveProperty('priority');
    });
    expect(lastQuery()).toMatchObject({ state: DEFAULT_STATES, sort: 'title' });
  });

  it('falls back to the due-date sort for an unknown stored value', async () => {
    window.localStorage.setItem('nf:my-tasks-sort', 'bogus');
    serve([row({ id: 'a', title: 'Alpha task' })]);
    await renderLoaded('Alpha task');

    expect(lastQuery()).toMatchObject({ sort: 'dueOn' });
  });
});

describe('/today filters — layout', () => {
  const rows = [
    row({ id: 'low', title: 'Low later', priority: 1, dueOn: '2999-01-01' }),
    row({ id: 'high', title: 'High later', priority: 4, dueOn: '2999-01-01' }),
    row({ id: 'late', title: 'Overdue one', priority: 0, dueOn: '2000-01-01' }),
    row({ id: 'none', title: 'No date', priority: 2 }),
  ];

  it('shows one ungrouped list in the server order for a non-date sort', async () => {
    window.localStorage.setItem('nf:my-tasks-sort', '-priority');
    serve([rows[1] as MyTaskListItem, rows[3] as MyTaskListItem, rows[0] as MyTaskListItem]);
    await renderLoaded('High later');

    expect(screen.queryByRole('heading', { level: 2 })).toBeNull();
    expect(titlesInOrder().map((s) => rows.find((r) => s.includes(r.title))?.id)).toEqual([
      'high',
      'none',
      'low',
    ]);
  });

  it('keeps the server order inside each due-date section', async () => {
    // The mock returns the lower priority first; the screen must not re-sort.
    serve([rows[0] as MyTaskListItem, rows[1] as MyTaskListItem, rows[2] as MyTaskListItem]);
    await renderLoaded('Low later');

    const headings = screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent);
    expect(headings[0]).toContain('today.sections.overdue');
    expect(headings[1]).toContain('today.sections.later');
    const later = screen.getByRole('region', { name: 'today.sections.later' });
    const order = within(later)
      .getAllByRole('link')
      .map((a) => a.textContent ?? '');
    expect(order[0]).toContain('Low later');
    expect(order[1]).toContain('High later');
  });

  it('orders the sections latest first for a descending due-date sort', async () => {
    window.localStorage.setItem('nf:my-tasks-sort', '-dueOn');
    serve(rows);
    await renderLoaded('Low later');

    const headings = screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent ?? '');
    const keys = ['later', 'overdue', 'noDue'].map((k) =>
      headings.findIndex((h) => h.includes(`today.sections.${k}`)),
    );
    expect(keys[0]).toBeLessThan(keys[1] as number);
    expect(keys[1]).toBeLessThan(keys[2] as number);
  });

  it('drops rows whose state is not selected', async () => {
    serve([
      row({ id: 'a', title: 'Still open' }),
      row({ id: 'b', title: 'Already done', derivedState: 'done' }),
    ]);
    await renderLoaded('Still open');

    expect(screen.queryByText('Already done')).toBeNull();
  });
});

describe('/today filters — empty and pending', () => {
  it('shows the plain empty state under the default filters', async () => {
    serve([]);
    await renderLoaded();

    expect(await screen.findByText('today.empty')).toBeTruthy();
    expect(screen.queryByText('today.filters.no_match')).toBeNull();
  });

  it('shows the no-match state, with the filter bar, when filters exclude everything', async () => {
    serve((query) => (query.priority ? [] : [row({ id: 'a', title: 'Alpha task' })]));
    await renderLoaded('Alpha task');

    const group = filterGroup('tasks.filters.priority');
    fireEvent.click(within(group).getByRole('button', { name: 'tasks.priority.urgent' }));

    expect(await screen.findByText('today.filters.no_match')).toBeTruthy();
    expect(screen.queryByText('today.empty')).toBeNull();
    expect(screen.getByRole('search')).toBeTruthy();
    expect(
      within(screen.getByRole('status')).getByRole('button', { name: 'tasks.filters.clear' }),
    ).toBeTruthy();
  });

  it('marks the list busy while the next result is loading', async () => {
    serve([row({ id: 'a', title: 'Alpha task' })]);
    const { container } = await renderLoaded('Alpha task');
    expect(container.querySelector('[aria-busy="true"]')).toBeNull();

    const gate = deferred<unknown>();
    mocks.get.mockImplementation(() => gate.promise);
    const group = filterGroup('tasks.filters.priority');
    fireEvent.click(within(group).getByRole('button', { name: 'tasks.priority.urgent' }));

    await waitFor(() => {
      expect(container.querySelector('[aria-busy="true"]')).not.toBeNull();
    });
    expect(screen.getByText('Alpha task')).toBeTruthy();

    gate.resolve(await ok({ tasks: [row({ id: 'b', title: 'Urgent task', priority: 4 })] }));
    await screen.findByText('Urgent task');
    await waitFor(() => {
      expect(container.querySelector('[aria-busy="true"]')).toBeNull();
    });
  });
});
