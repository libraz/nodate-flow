/** Thumbnails and gallery in the task attachment list. */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Suspense } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { TaskAttachment } from '../api';
import TaskAttachments from '../task-attachments';

const getMock = vi.fn();

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: (...args: unknown[]) => getMock(...args) },
  authSdk: { GET: (...args: unknown[]) => getMock(...args) },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, string>) =>
      key === 'gallery.labels.index_of_total'
        ? `${opts?.index} of ${opts?.total}`
        : opts?.filename
          ? `${key}:${opts.filename}`
          : key,
    i18n: { resolvedLanguage: 'en' },
  }),
}));

function attachment(id: string, filename: string, patch: Partial<TaskAttachment>): TaskAttachment {
  return {
    id,
    filename,
    byteSize: 1024,
    contentType: 'image/png',
    createdAt: 1,
    previewable: true,
    storageKey: `k-${id}`,
    uploaderDisplayName: 'Sam',
    uploaderId: 'u1',
    ...patch,
  };
}

const attachments = [
  attachment('a1', 'a.png', {}),
  attachment('b1', 'b.png', {}),
  attachment('p1', 'doc.pdf', { contentType: 'application/pdf', previewable: false }),
  attachment('c1', 'big.png', { byteSize: 10 * 1024 * 1024 + 1 }),
];

function reply(data: unknown): unknown {
  return { data, error: undefined, response: new Response(null, { status: 200 }) };
}

function paths(): string[] {
  return getMock.mock.calls.map((call) => String(call[0]));
}

function previewCallsFor(aid: string): number {
  return getMock.mock.calls.filter(
    (call) =>
      call[0] === '/tasks/{id}/attachments/{aid}/preview' &&
      (call[1] as { params: { path: { aid: string } } }).params.path.aid === aid,
  ).length;
}

beforeEach(() => {
  getMock.mockReset();
  getMock.mockImplementation(async (path: string, init: { params: { path: { aid?: string } } }) => {
    if (path === '/tasks/{id}/attachments') return reply({ attachments });
    if (path === '/tasks/{id}/attachments/{aid}/preview') {
      return reply({
        previewUrl: `https://blob.example/${init.params.path.aid}.png`,
        expiresAt: Math.floor(Date.now() / 1000) + 1800,
      });
    }
    if (path === '/tasks/{id}/attachments/{aid}/download') {
      return reply({ downloadUrl: `https://blob.example/dl/${init.params.path.aid}` });
    }
    throw new Error(`unexpected GET ${path}`);
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

function renderList(): void {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <Suspense fallback={null}>
        <TaskAttachments taskId="t1" />
      </Suspense>
    </QueryClientProvider>,
  );
}

describe('TaskAttachments previews', () => {
  it('shows thumbnails for previewable rows only and does not load oversized ones', async () => {
    renderList();
    await screen.findByRole('button', { name: 'gallery.open_preview:a.png' });
    expect(screen.getByRole('button', { name: 'gallery.open_preview:b.png' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'gallery.open_preview:doc.pdf' })).toBeNull();

    const oversized = screen.getByRole('button', { name: 'gallery.open_preview:big.png' });
    expect(oversized.getAttribute('title')).toBe('gallery.too_large_hint');

    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: 'gallery.open_preview:a.png' }).querySelector('img'),
      ).not.toBeNull();
    });
    expect(previewCallsFor('a1')).toBe(1);
    expect(previewCallsFor('c1')).toBe(0);
    expect(paths()).not.toContain('/tasks/{id}/attachments/{aid}/download');
  });

  it('opens the gallery at the clicked image and downloads through the download API', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    renderList();
    await userEvent.click(
      await screen.findByRole('button', { name: 'gallery.open_preview:b.png' }),
    );

    const download = await screen.findByRole('button', { name: 'gallery.labels.download' });
    await userEvent.click(download);

    await waitFor(() => {
      expect(open).toHaveBeenCalledWith('https://blob.example/dl/b1', '_blank');
    });
    // The oversized image joins the gallery once the user opens it.
    expect(previewCallsFor('c1')).toBe(1);
  });
});
