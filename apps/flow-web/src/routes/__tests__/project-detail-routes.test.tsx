/** Overview, members and settings are independent routes with no inner tablist. */

import { render as rtlRender, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: { component: unknown }) => ({
    options,
    useParams: () => ({ id: 'ws-1', projectId: 'p-1' }),
  }),
}));

vi.mock('../../features/projects/project-detail', () => ({
  OverviewPanel: () => <div data-testid="overview" />,
  SettingsPanel: () => <div data-testid="settings" />,
}));

vi.mock('../../features/projects/project-members-table', () => ({
  default: () => <div data-testid="members" />,
}));

import { Route as IndexRoute } from '../_authenticated.workspaces.$id.projects.$projectId.index';
import { Route as MembersRoute } from '../_authenticated.workspaces.$id.projects.$projectId.members';
import { Route as SettingsRoute } from '../_authenticated.workspaces.$id.projects.$projectId.settings';

function render(route: unknown, testId: string): void {
  const Component = (route as { options: { component: () => ReactNode } }).options.component;
  rtlRender(<Component />);
  expect(screen.getByTestId(testId)).toBeTruthy();
  expect(screen.queryByRole('tablist')).toBeNull();
}

describe('project detail routes', () => {
  it('renders the overview without tabs', () => {
    render(IndexRoute, 'overview');
  });
  it('renders the members table without tabs', () => {
    render(MembersRoute, 'members');
  });
  it('renders the settings panel without tabs', () => {
    render(SettingsRoute, 'settings');
  });
});
