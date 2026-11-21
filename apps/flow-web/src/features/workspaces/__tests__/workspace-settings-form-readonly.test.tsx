/**
 * The general settings form follows the caller's role: admins edit and save,
 * members and guests see it read-only with a note, and an unknown role gets
 * read-only fields without the note (no flicker while the role loads).
 */

import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ role: null as string | null }));

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({ toaster: { show: vi.fn() } }));

vi.mock('../../../components/page-header', () => ({
  default: ({ title }: { title: string }) => <h1>{title}</h1>,
}));

vi.mock('../../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => mocks.role,
  isWorkspaceAdmin: (role: string | null) => role === 'owner' || role === 'admin',
}));

vi.mock('../api', () => ({
  useWorkspaceQuery: () => ({
    data: { id: 'ws-1', name: 'Acme', slug: 'acme', description: null, iconUrl: null },
  }),
  useWorkspaceMembersQuery: () => ({ data: [] }),
  useUpdateWorkspace: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteWorkspace: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

import WorkspaceSettingsForm from '../workspace-settings-form';

const NOTE = 'workspace.general.read_only_note';
const SAVE = 'workspace.general.save';
const DELETE = 'workspace.general.danger.delete';

beforeEach(() => {
  mocks.role = null;
});

describe('WorkspaceSettingsForm role handling', () => {
  it.each(['member', 'guest'])('is read-only with a note for %s', (role) => {
    mocks.role = role;
    render(<WorkspaceSettingsForm workspaceId="ws-1" />);
    expect(screen.getByText(NOTE)).toBeTruthy();
    expect(screen.queryByRole('button', { name: SAVE })).toBeNull();
    for (const box of screen.getAllByRole('textbox')) {
      expect((box as HTMLInputElement).disabled).toBe(true);
    }
  });

  it('is read-only without a note while the role is unknown', () => {
    render(<WorkspaceSettingsForm workspaceId="ws-1" />);
    expect(screen.queryByText(NOTE)).toBeNull();
    expect(screen.queryByRole('button', { name: SAVE })).toBeNull();
    for (const box of screen.getAllByRole('textbox')) {
      expect((box as HTMLInputElement).disabled).toBe(true);
    }
  });

  it('lets an admin edit and save but not delete', () => {
    mocks.role = 'admin';
    render(<WorkspaceSettingsForm workspaceId="ws-1" />);
    expect(screen.queryByText(NOTE)).toBeNull();
    expect(screen.getByRole('button', { name: SAVE })).toBeTruthy();
    expect(screen.queryByRole('button', { name: DELETE })).toBeNull();
  });

  it('shows the delete action to an owner only', () => {
    mocks.role = 'owner';
    render(<WorkspaceSettingsForm workspaceId="ws-1" />);
    expect(screen.getByRole('button', { name: DELETE })).toBeTruthy();
  });
});
