import { QueryClient } from '@tanstack/react-query';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { Suspense } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn(), toastShow: vi.fn() }));

vi.mock('../../../lib/sdk', () => ({
  sdk: { POST: mocks.post, GET: mocks.get },
  authSdk: { POST: mocks.post, GET: mocks.get },
}));
vi.mock('@nodate-flow/ui/primitives/toast', () => ({ toaster: { show: mocks.toastShow } }));

import { type Project, projectsKeys } from '../api';
import ProjectArchiveActions from '../project-archive-actions';

function renderActions(isArchived: boolean): void {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Number.POSITIVE_INFINITY } },
  });
  queryClient.setQueryData(projectsKeys.detail('prj-1'), {
    id: 'prj-1',
    name: 'Platform',
    isArchived,
  } as Project);
  renderWithProviders(
    <Suspense fallback={null}>
      <ProjectArchiveActions id="prj-1" />
    </Suspense>,
    { queryClient },
  );
}

beforeEach(() => {
  mocks.post.mockReset().mockResolvedValue({
    data: { ok: true },
    error: null,
    response: new Response(null, { status: 200 }),
  });
  mocks.toastShow.mockReset();
});

describe('ProjectArchiveActions', () => {
  it('archives without confirmation and offers Undo that restores', async () => {
    const user = userEvent.setup();
    renderActions(false);

    await user.click(await screen.findByRole('button', { name: 'projects.archive.action' }));

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledWith('/projects/{prjId}/archive', {
        params: { path: { prjId: 'prj-1' } },
      });
    });
    const toast = mocks.toastShow.mock.calls[0]?.[0] as {
      tone: string;
      action: { label: string; onAction: () => void };
    };
    expect(toast.tone).toBe('success');
    expect(toast.action.label).toBe('common.undo');

    toast.action.onAction();
    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledWith('/projects/{prjId}/unarchive', {
        params: { path: { prjId: 'prj-1' } },
      });
    });
  });

  it('shows the restore label when archived and unarchives on click', async () => {
    const user = userEvent.setup();
    renderActions(true);

    expect(screen.queryByRole('button', { name: 'projects.archive.action' })).toBeNull();
    await user.click(await screen.findByRole('button', { name: 'projects.archive.restore' }));

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledWith('/projects/{prjId}/unarchive', {
        params: { path: { prjId: 'prj-1' } },
      });
    });
  });
});
