/**
 * The authenticated 404 page keeps the shell's global behaviour: `?` opens
 * the shortcuts list and the task-create trigger reaches the dialogs.
 */

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ projectId: null as string | null }));

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: unknown) => ({ options }),
  Outlet: () => null,
  useNavigate: () => vi.fn(),
}));

vi.mock('../../components/layout/app-shell', () => ({
  default: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));
vi.mock('../../components/not-found', () => ({ default: () => <p>not found</p> }));
vi.mock('../../features/glass-dock/glass-dock', () => ({ default: () => null }));
vi.mock('../../features/auth/use-auth-bootstrap', () => ({
  useAuthBootstrap: () => ({ status: 'authenticated', retry: vi.fn() }),
}));
vi.mock('../../lib/use-default-project', () => ({
  useDefaultProjectId: () => ({ projectId: mocks.projectId, workspaceId: 'ws-1' }),
}));
vi.mock('../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => 'admin',
  isWorkspaceAdmin: () => true,
}));
vi.mock('../../components/shortcuts-help-dialog', () => ({
  default: ({ open }: { open: boolean }) => (open ? <div role="dialog">shortcuts</div> : null),
}));
vi.mock('../../features/tasks/task-create-dialog', () => ({
  default: ({ open }: { open: boolean }) => (open ? <div role="dialog">task create</div> : null),
}));
vi.mock('../../features/tasks/quick-capture-dialog', () => ({ default: () => null }));
vi.mock('../../features/tasks/no-project-hint-dialog', () => ({
  default: ({ open }: { open: boolean }) => (open ? <div role="dialog">no project</div> : null),
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import { OPEN_CREATE_TASK_EVENT } from '../../components/layout/glass-dock';
import { Route } from '../_authenticated';

function renderNotFound(): void {
  const Component = (Route as unknown as { options: { notFoundComponent: () => ReactNode } })
    .options.notFoundComponent;
  render(<Component />);
}

describe('authenticated 404 page', () => {
  it('opens the shortcuts list on "?"', async () => {
    renderNotFound();
    await userEvent.setup().keyboard('?');
    expect(screen.getByRole('dialog').textContent).toBe('shortcuts');
  });

  it('opens the task-create dialog on "c" when a project is reachable', async () => {
    mocks.projectId = 'p-1';
    renderNotFound();
    await userEvent.setup().keyboard('c');
    expect(screen.getByRole('dialog').textContent).toBe('task create');
  });

  it('opens the no-project guidance from the dock event when none exists', () => {
    mocks.projectId = null;
    renderNotFound();
    window.dispatchEvent(new Event(OPEN_CREATE_TASK_EVENT));
    return screen.findByText('no project');
  });
});
