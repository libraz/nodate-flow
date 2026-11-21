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

import type { Project } from '../api';
import ArchivedProjectsSection from '../archived-projects-section';

const live = { id: 'prj-live', name: 'Live project', isArchived: false } as Project;
const old = { id: 'prj-old', name: 'Old project', isArchived: true } as Project;

beforeEach(() => {
  mocks.post.mockReset().mockResolvedValue({
    data: { ok: true },
    error: null,
    response: new Response(null, { status: 200 }),
  });
  mocks.toastShow.mockReset();
});

describe('ArchivedProjectsSection', () => {
  it('renders nothing when no project is archived', () => {
    const { container } = renderWithProviders(
      <ArchivedProjectsSection workspaceId="ws-1" projects={[live]} />,
    );
    expect(container.textContent).toBe('');
  });

  it('lists only archived projects and restores one', async () => {
    const user = userEvent.setup();
    renderWithProviders(<ArchivedProjectsSection workspaceId="ws-1" projects={[live, old]} />);

    expect(screen.getByText('projects.title')).toBeDefined();
    expect(screen.getByText('Old project')).toBeDefined();
    expect(screen.queryByText('Live project')).toBeNull();

    await user.click(screen.getByRole('button', { name: 'projects.archive.restore' }));
    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledWith('/projects/{prjId}/unarchive', {
        params: { path: { prjId: 'prj-old' } },
      });
    });
  });
});
