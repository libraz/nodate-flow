/**
 * A Japanese-only project name must be creatable without touching the
 * slug / identifier fields, which sit in a collapsed disclosure.
 */

import { fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const h = vi.hoisted(() => ({ mutateAsync: vi.fn() }));

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, useCreateProject: () => ({ mutateAsync: h.mutateAsync, isPending: false }) };
});

vi.mock('@nodate-flow/ui/primitives/toast', () => ({ toaster: { show: vi.fn() } }));

import ProjectCreateDialog from '../project-create-dialog';

beforeEach(() => {
  h.mutateAsync.mockReset();
});

describe('ProjectCreateDialog advanced fields', () => {
  it('submits generated slug and identifier for a Japanese-only name', async () => {
    const created = { id: 'prj-1' };
    h.mutateAsync.mockResolvedValue(created);
    const onCreated = vi.fn();
    const { container } = renderWithProviders(
      <ProjectCreateDialog workspaceId="ws-1" open onClose={vi.fn()} onCreated={onCreated} />,
    );

    fireEvent.change(screen.getByRole('textbox', { name: /projects\.form\.name/ }), {
      target: { value: '営業 Q4' },
    });
    expect(container.ownerDocument.querySelector('details')?.open).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'projects.form.submit' }));

    await waitFor(() => {
      expect(h.mutateAsync).toHaveBeenCalled();
    });
    const input = h.mutateAsync.mock.calls[0]?.[0].input as { slug: string; identifier: string };
    expect(input.slug).toMatch(/^project-[a-z0-9]{6}$/);
    expect(input.identifier).toMatch(/^P[A-Z0-9]{3}$/);
    expect(onCreated).toHaveBeenCalledWith(created);
    expect(container.ownerDocument.querySelector('details')?.open).toBe(false);
  });

  it('falls back to a P-prefixed identifier when the name has no Latin letters', async () => {
    h.mutateAsync.mockResolvedValue({ id: 'prj-2' });
    renderWithProviders(<ProjectCreateDialog workspaceId="ws-1" open onClose={vi.fn()} />);
    fireEvent.change(screen.getByRole('textbox', { name: /projects\.form\.name/ }), {
      target: { value: '営業' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'projects.form.submit' }));
    await waitFor(() => {
      expect(h.mutateAsync).toHaveBeenCalled();
    });
    const input = h.mutateAsync.mock.calls[0]?.[0].input as { identifier: string };
    expect(input.identifier).toMatch(/^P[A-Z0-9]{3}$/);
  });

  it('opens the disclosure when the server reports a taken slug', async () => {
    h.mutateAsync.mockRejectedValue({ code: 'WS.PROJECT.SLUG_ALREADY_TAKEN' });
    const { container } = renderWithProviders(
      <ProjectCreateDialog workspaceId="ws-1" open onClose={vi.fn()} />,
    );
    fireEvent.change(screen.getByRole('textbox', { name: /projects\.form\.name/ }), {
      target: { value: '営業' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'projects.form.submit' }));
    await waitFor(() => {
      expect(container.ownerDocument.querySelector('details')?.open).toBe(true);
    });
  });
});
