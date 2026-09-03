/**
 * Removing a member from a project.
 *
 * `DELETE /projects/{prjId}/members/{userId}` has always existed and the
 * table had no action column, so a mis-added member or someone who has
 * left could not be detached from a project without going to the API by
 * hand. The gating mirrors WorkspaceMembersTable: derived from the member
 * list already loaded, hidden for callers without the privilege, and
 * withheld for the rows whose removal would strand the project.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement, ReactNode } from 'react';
import { Suspense } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  del: vi.fn(),
  toastShow: vi.fn(),
  currentUserId: 'user-lead',
}));

vi.mock('../../../lib/sdk', () => ({
  // openapi-fetch keys its client by HTTP method, hence the caps.
  sdk: { GET: mocks.get, DELETE: mocks.del },
  authSdk: { GET: mocks.get, DELETE: mocks.del },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

vi.mock('../../auth/auth-store', () => ({
  selectUser: (s: unknown) => s,
  useAuth: () => ({ id: mocks.currentUserId }),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { resolvedLanguage: 'en' } }),
}));

vi.mock('../project-add-member-dialog', () => ({
  default: (): null => null,
}));

import ProjectMembersTable from '../project-members-table';

/* ── fixtures ─────────────────────────────────────────────────── */

interface Member {
  id: string;
  userId: string;
  email: string;
  displayName: string;
  role: string;
  createdAt: number;
}

function member(userId: string, role: string): Member {
  return {
    id: `pm-${userId}`,
    userId,
    email: `${userId}@example.com`,
    displayName: userId,
    role,
    createdAt: 1_700_000_000,
  };
}

function serveMembers(members: Member[]): void {
  mocks.get.mockResolvedValue({
    data: { members, total: members.length },
    error: null,
    response: new Response(null, { status: 200 }),
  });
}

function renderTable(): void {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={client}>
        <Suspense fallback={null}>{children}</Suspense>
      </QueryClientProvider>
    );
  }
  render(
    <Wrapper>
      <ProjectMembersTable projectId="prj-1" />
    </Wrapper>,
  );
}

beforeEach(() => {
  mocks.get.mockReset();
  mocks.del
    .mockReset()
    .mockResolvedValue({ error: null, response: new Response(null, { status: 200 }) });
  mocks.toastShow.mockReset();
  mocks.currentUserId = 'user-lead';
});

describe('ProjectMembersTable removal', () => {
  it('offers removal to a project lead', async () => {
    serveMembers([member('user-lead', 'lead'), member('user-editor', 'editor')]);
    renderTable();

    expect(await screen.findByText('user-editor@example.com')).toBeDefined();
    expect(screen.getByRole('button', { name: 'projects.members.remove' })).toBeDefined();
  });

  it('calls the endpoint with the member being removed', async () => {
    const user = userEvent.setup();
    serveMembers([member('user-lead', 'lead'), member('user-editor', 'editor')]);
    renderTable();

    await user.click(await screen.findByRole('button', { name: 'projects.members.remove' }));

    expect(mocks.del).toHaveBeenCalledTimes(1);
    const [path, init] = mocks.del.mock.calls[0] as [string, { params: unknown }];
    expect(path).toBe('/projects/{prjId}/members/{userId}');
    expect(init.params).toEqual({ path: { prjId: 'prj-1', userId: 'user-editor' } });
  });

  it('hides the action from a member who cannot use it', async () => {
    // An editor is not a project admin, so the endpoint would refuse —
    // a visible button here is a button that only ever errors.
    mocks.currentUserId = 'user-editor';
    serveMembers([member('user-lead', 'lead'), member('user-editor', 'editor')]);
    renderTable();

    expect(await screen.findByText('user-editor@example.com')).toBeDefined();
    expect(screen.queryByRole('button', { name: 'projects.members.remove' })).toBeNull();
  });

  it('does not offer to remove yourself', async () => {
    serveMembers([member('user-lead', 'lead'), member('user-other-lead', 'lead')]);
    renderTable();

    expect(await screen.findByText('user-lead@example.com')).toBeDefined();
    // Two leads, so the other one is removable; the caller's own row is
    // not, which leaves exactly one button.
    expect(screen.getAllByRole('button', { name: 'projects.members.remove' })).toHaveLength(1);
  });

  it('does not offer to remove the last lead', async () => {
    serveMembers([member('user-lead', 'lead')]);
    renderTable();

    expect(await screen.findByText('user-lead@example.com')).toBeDefined();
    expect(screen.queryByRole('button', { name: 'projects.members.remove' })).toBeNull();
  });

  it('reports a failed removal rather than looking like it worked', async () => {
    const user = userEvent.setup();
    serveMembers([member('user-lead', 'lead'), member('user-editor', 'editor')]);
    mocks.del.mockResolvedValue({
      error: { type: 'WS.MEMBER.NOT_FOUND', detail: 'gone', status: 404 },
      response: new Response(null, { status: 400 }),
    });
    renderTable();

    await user.click(await screen.findByRole('button', { name: 'projects.members.remove' }));

    expect(mocks.toastShow).toHaveBeenCalledWith(expect.objectContaining({ tone: 'danger' }));
  });
});
