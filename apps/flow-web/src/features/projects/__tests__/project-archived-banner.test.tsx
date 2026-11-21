import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ post: vi.fn(), toastShow: vi.fn() }));

vi.mock('../../../lib/sdk', () => ({
  sdk: { POST: mocks.post },
  authSdk: { POST: mocks.post },
}));
vi.mock('@nodate-flow/ui/primitives/toast', () => ({ toaster: { show: mocks.toastShow } }));

import ProjectArchivedBanner from '../project-archived-banner';

beforeEach(() => {
  mocks.post.mockReset().mockResolvedValue({
    data: { ok: true },
    error: null,
    response: new Response(null, { status: 200 }),
  });
  mocks.toastShow.mockReset();
});

describe('ProjectArchivedBanner', () => {
  it('renders nothing for a project that is not archived', () => {
    const { container } = renderWithProviders(
      <ProjectArchivedBanner projectId="prj-1" projectName="Platform" isArchived={false} />,
    );
    expect(container.textContent).toBe('');
  });

  it('shows the notice and restores through unarchive', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <ProjectArchivedBanner projectId="prj-1" projectName="Platform" isArchived />,
    );

    expect(screen.getByText('projects.archived_banner.text')).toBeDefined();
    await user.click(screen.getByRole('button', { name: 'projects.archive.restore' }));

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledWith('/projects/{prjId}/unarchive', {
        params: { path: { prjId: 'prj-1' } },
      });
    });
    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalledWith(expect.objectContaining({ tone: 'success' }));
    });
  });
});
