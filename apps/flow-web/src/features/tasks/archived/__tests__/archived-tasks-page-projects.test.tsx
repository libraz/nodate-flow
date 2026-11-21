/**
 * With archived projects but no archived tasks the page shows a single
 * line instead of the large "nothing stored" state.
 */

import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ projects: [] as unknown[] }));

vi.mock('../hooks/use-archived-tasks', () => ({
  useArchivedTasksQuery: () => ({
    tasks: [],
    total: 0,
    hasNextPage: false,
    fetchNextPage: vi.fn(),
    isFetchingNextPage: false,
  }),
}));
vi.mock('../../../projects/api', () => ({
  useProjectsQuery: () => ({ data: mocks.projects }),
  useUnarchiveProject: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock('../../../workspaces/api', () => ({ useWorkspaceMembersQuery: () => ({ data: [] }) }));
vi.mock('../../api', () => ({
  useUnarchiveTask: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock('../archived-header', () => ({ default: () => null }));
vi.mock('../archived-filters', () => ({ default: () => null }));
vi.mock('../archived-bulk-action-bar', () => ({ default: () => null }));
vi.mock('../archived-empty-state', () => ({
  default: () => <p>big-empty-state</p>,
}));

import ArchivedTasksPage from '../archived-tasks-page';

beforeEach(() => {
  mocks.projects = [];
});

describe('ArchivedTasksPage with projects', () => {
  it('shows the one-line empty note when only projects are archived', () => {
    mocks.projects = [{ id: 'prj-1', name: 'Old project', isArchived: true }];
    renderWithProviders(<ArchivedTasksPage workspaceId="ws-1" />);

    expect(screen.getByText('empty.noTasks')).toBeDefined();
    expect(screen.getByText('Old project')).toBeDefined();
    expect(screen.queryByText('big-empty-state')).toBeNull();
  });

  it('keeps the large empty state when nothing at all is archived', () => {
    mocks.projects = [{ id: 'prj-1', name: 'Live', isArchived: false }];
    renderWithProviders(<ArchivedTasksPage workspaceId="ws-1" />);

    expect(screen.getByText('big-empty-state')).toBeDefined();
    expect(screen.queryByText('empty.noTasks')).toBeNull();
  });
});
