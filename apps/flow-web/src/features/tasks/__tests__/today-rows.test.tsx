/**
 * /today rows: a complete control only where the caller is the assignee,
 * an equally wide spacer elsewhere, one row per task, and the control kept
 * outside the row link.
 */

import { QueryClient } from '@tanstack/react-query';
import { screen, within } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { type ReactElement, Suspense } from 'react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: vi.fn(), POST: vi.fn() },
  authSdk: { GET: vi.fn(), POST: vi.fn() },
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
import { type MyTaskListItem, myTasksKey } from '../api';
import { DEFAULT_MY_TASK_FILTERS } from '../use-my-task-filters';

const TodayRoute = Route.options.component as () => ReactElement;

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

function renderToday(rows: MyTaskListItem[]) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, throwOnError: false } },
  });
  queryClient.setQueryData(myTasksKey({ filters: DEFAULT_MY_TASK_FILTERS, sort: 'dueOn' }), rows);
  return renderWithProviders(
    <Suspense fallback={null}>
      <TodayRoute />
    </Suspense>,
    { queryClient },
  );
}

/** The row grid inside the list item (the item itself is a presence wrapper). */
function itemFor(title: string): HTMLElement {
  const row = screen.getByText(title).closest('li')?.firstElementChild?.firstElementChild;
  if (!(row instanceof HTMLElement)) throw new Error(`no row for ${title}`);
  return row;
}

describe('/today rows', () => {
  it('puts a complete button on rows the caller is assigned to', () => {
    renderToday([row({ id: 'a', title: 'Assigned task' })]);

    const button = within(itemFor('Assigned task')).getByRole('button', {
      name: 'today.complete_task',
    });
    expect(button.closest('a')).toBeNull();
  });

  it('shows an enabled complete button on an in-progress assigned row', () => {
    renderToday([row({ id: 'a', title: 'Busy task', derivedState: 'waiting' })]);

    const button = within(itemFor('Busy task')).getByRole('button', {
      name: 'today.complete_task',
    });
    expect(button.getAttribute('aria-disabled')).toBeNull();
  });

  it.each(['reviewer', 'approver', 'watcher'])(
    'has no button on a %s row, only a spacer before the link',
    (actorRole) => {
      renderToday([row({ id: 'r', title: 'Reviewed task', actorRole })]);

      const item = itemFor('Reviewed task');
      expect(within(item).queryByRole('button')).toBeNull();
      const [lead, link] = Array.from(item.children);
      expect(lead?.tagName).toBe('SPAN');
      expect(lead?.getAttribute('aria-hidden')).toBe('true');
      expect(lead?.childElementCount).toBe(0);
      expect(link?.tagName).toBe('A');
    },
  );

  it('keeps the spacer and the button in the same first column', () => {
    renderToday([
      row({ id: 'a', title: 'Mine' }),
      row({ id: 'r', title: 'Theirs', actorRole: 'reviewer' }),
    ]);

    const mine = itemFor('Mine');
    const theirs = itemFor('Theirs');
    expect(mine.children).toHaveLength(2);
    expect(theirs.children).toHaveLength(2);
    expect(mine.children[1]?.tagName).toBe('A');
    expect(theirs.children[1]?.tagName).toBe('A');
  });

  it('renders a task held in two roles once, with the assignee control', () => {
    renderToday([
      row({ id: 'dup', title: 'Both roles', actorRole: 'reviewer' }),
      row({ id: 'dup', title: 'Both roles', actorRole: 'assignee' }),
    ]);

    expect(screen.getAllByText('Both roles')).toHaveLength(1);
    expect(
      within(itemFor('Both roles')).getByRole('button', { name: 'today.complete_task' }),
    ).toBeTruthy();
  });
});
