/**
 * Getting-started checklist: who sees it, which step is primary, and
 * that a failing invite lookup leaves the card standing.
 */

import { fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { Suspense } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const h = vi.hoisted(() => ({
  handlers: {} as Record<string, () => unknown>,
  role: 'admin' as string | null,
}));

vi.mock('../../../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../../../lib/api')>('../../../lib/api');
  const client = {
    GET: async (path: string) => {
      const handler = h.handlers[path];
      if (!handler) throw new Error(`unmocked ${path}`);
      return handler();
    },
  };
  const request = async (call: (c: typeof client) => Promise<unknown>) => call(client);
  return { ...actual, apiRequest: request, authApiRequest: request };
});

vi.mock('../../../lib/use-current-workspace', async () => {
  const actual = await vi.importActual<typeof import('../../../lib/use-current-workspace')>(
    '../../../lib/use-current-workspace',
  );
  return { ...actual, useCurrentWorkspaceId: () => 'ws-1' };
});

vi.mock('../../../lib/use-current-workspace-role', async () => {
  const actual = await vi.importActual<typeof import('../../../lib/use-current-workspace-role')>(
    '../../../lib/use-current-workspace-role',
  );
  return { ...actual, useCurrentWorkspaceRole: () => h.role };
});

vi.mock('../../auth/auth-store', () => ({
  selectUser: (s: { user: unknown }) => s.user,
  useAuth: (select: (s: { user: { id: string } }) => unknown) => select({ user: { id: 'u1' } }),
}));

import OnboardingChecklist from '../onboarding-checklist';

const project = { id: 'p1', name: 'P', slug: 'p', identifier: 'P', isArchived: false };

function setup(overrides: Record<string, () => unknown> = {}): void {
  h.handlers = {
    '/workspaces': () => ({ workspaces: [{ id: 'ws-1', name: 'Acme' }] }),
    '/workspaces/{wsId}/projects': () => ({ projects: [] }),
    '/workspaces/{wsId}/members': () => ({ members: [{ userId: 'u1' }] }),
    '/workspaces/{wsId}/invites': () => ({ invites: [] }),
    '/tasks': () => ({ tasks: [] }),
    ...overrides,
  };
  renderWithProviders(
    <Suspense fallback={null}>
      <OnboardingChecklist />
    </Suspense>,
  );
}

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 20));
}

beforeEach(() => {
  h.role = 'admin';
  window.localStorage.clear();
});

describe('OnboardingChecklist', () => {
  it('points an admin with no project at the project step as the primary action', async () => {
    setup();
    const projectButton = await screen.findByRole('button', {
      name: 'onboarding.steps.project.title',
    });
    expect(projectButton.className).not.toMatch(/textLink/);
    // Later incomplete steps are demoted to text actions.
    expect(screen.getByRole('button', { name: 'onboarding.steps.task.title' }).className).toMatch(
      /textLink/,
    );
    expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('1');
    expect(screen.getByRole('progressbar').getAttribute('aria-valuemax')).toBe('4');
  });

  it('is hidden from members', async () => {
    h.role = 'member';
    setup();
    await settle();
    expect(screen.queryByRole('progressbar')).toBeNull();
  });

  it('shows only the workspace step as incomplete when there is no workspace', async () => {
    h.role = null;
    setup({ '/workspaces': () => ({ workspaces: [] }) });
    const link = await screen.findByRole('link', { name: 'onboarding.steps.workspace.title' });
    expect(link.className).not.toMatch(/textLink/);
    expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('0');
    expect(screen.queryByRole('button', { name: 'onboarding.steps.project.title' })).toBeNull();
  });

  it('dismisses and remembers the choice per user', async () => {
    setup();
    fireEvent.click(await screen.findByRole('button', { name: 'onboarding.dismiss' }));
    expect(window.localStorage.getItem('nf.onboarding.dismissed.u1')).toBe('1');
    expect(screen.queryByRole('progressbar')).toBeNull();
  });

  it('hides itself once all four steps are complete', async () => {
    setup({
      '/workspaces/{wsId}/projects': () => ({ projects: [project] }),
      '/tasks': () => ({ tasks: [{ id: 't1' }] }),
      '/workspaces/{wsId}/members': () => ({ members: [{ userId: 'u1' }, { userId: 'u2' }] }),
    });
    await waitFor(() => {
      expect(screen.queryByRole('progressbar')).toBeNull();
    });
    await settle();
    expect(screen.queryByRole('progressbar')).toBeNull();
  });

  it('keeps rendering with the invite step incomplete when the invite list is refused', async () => {
    setup({
      '/workspaces/{wsId}/projects': () => ({ projects: [project] }),
      '/tasks': () => ({ tasks: [{ id: 't1' }] }),
      '/workspaces/{wsId}/invites': () => {
        throw new Error('403');
      },
    });
    await waitFor(() => {
      expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('3');
    });
    expect(screen.getByRole('link', { name: 'onboarding.steps.invite.title' })).not.toBeNull();
  });
});
