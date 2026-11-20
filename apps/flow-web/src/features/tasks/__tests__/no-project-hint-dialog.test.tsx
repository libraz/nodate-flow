/**
 * The three answers to "new task" with no project in reach: create a
 * workspace, create a project, or ask an admin.
 */

import { fireEvent, screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../../projects/project-create-dialog', () => ({
  default: ({
    open,
    onCreated,
  }: {
    open: boolean;
    onCreated?: (project: { id: string }) => void;
  }) =>
    open ? (
      <button type="button" onClick={() => onCreated?.({ id: 'prj-9' })}>
        mock-create-project
      </button>
    ) : null,
}));

import NoProjectHintDialog from '../no-project-hint-dialog';

describe('NoProjectHintDialog', () => {
  it('links to /setup when there is no workspace', () => {
    renderWithProviders(
      <NoProjectHintDialog
        open
        onClose={vi.fn()}
        workspaceId={null}
        canCreateProject={false}
        onProjectCreated={vi.fn()}
      />,
    );
    expect(screen.getByText('tasks.quick_create.no_workspace.title')).not.toBeNull();
    expect(screen.getByRole('link', { name: 'nav.noWorkspace.cta' }).getAttribute('href')).toBe(
      '/setup',
    );
  });

  it('lets an admin create a project and reports its id', () => {
    const onProjectCreated = vi.fn();
    renderWithProviders(
      <NoProjectHintDialog
        open
        onClose={vi.fn()}
        workspaceId="ws-1"
        canCreateProject
        onProjectCreated={onProjectCreated}
      />,
    );
    expect(screen.getByText('tasks.quick_create.no_projects.title')).not.toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'tasks.quick_create.no_projects.cta' }));
    fireEvent.click(screen.getByRole('button', { name: 'mock-create-project' }));
    expect(onProjectCreated).toHaveBeenCalledWith('prj-9');
  });

  it('tells a member to ask an admin and offers no create action', () => {
    renderWithProviders(
      <NoProjectHintDialog
        open
        onClose={vi.fn()}
        workspaceId="ws-1"
        canCreateProject={false}
        onProjectCreated={vi.fn()}
      />,
    );
    expect(screen.getByText('tasks.quick_create.no_projects.member_body')).not.toBeNull();
    expect(screen.queryByRole('button', { name: 'tasks.quick_create.no_projects.cta' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'nav.noWorkspace.cta' })).toBeNull();
  });
});
