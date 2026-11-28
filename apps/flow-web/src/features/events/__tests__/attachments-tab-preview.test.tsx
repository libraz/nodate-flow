/** Thumbnails and gallery in the calendar event attachments tab. */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Suspense } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { EventAttachment } from '../attachments-api';
import AttachmentsTab from '../attachments-tab';

const apiRequestMock = vi.fn();

vi.mock('../../../lib/api', () => ({
  apiRequest: (...args: unknown[]) => apiRequestMock(...args),
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

function attachment(
  id: string,
  filename: string,
  patch: Partial<EventAttachment>,
): EventAttachment {
  return {
    id,
    filename,
    byteSize: 1024,
    contentType: 'image/png',
    createdAt: 1,
    previewable: true,
    uploaderName: 'Sam',
    ...patch,
  } as EventAttachment;
}

const attachments = [
  attachment('a1', 'a.png', {}),
  attachment('b1', 'b.png', {}),
  attachment('p1', 'doc.pdf', { contentType: 'application/pdf', previewable: false }),
  attachment('c1', 'big.png', { byteSize: 10 * 1024 * 1024 + 1 }),
];

/** Every GET the component issued, in order. */
const recorded: { calls: { path: string; attId?: string }[] } = { calls: [] };

beforeEach(() => {
  recorded.calls.length = 0;
  apiRequestMock.mockReset();
  apiRequestMock.mockImplementation(async (call: (client: unknown) => unknown) => {
    const reply = (data: unknown): Promise<unknown> =>
      Promise.resolve({ data, error: undefined, response: new Response(null, { status: 200 }) });
    const client = {
      GET: (path: string, init: { params: { path: { attId?: string } } }) => {
        recorded.calls.push({
          path,
          ...(init.params.path.attId ? { attId: init.params.path.attId } : {}),
        });
        if (path.endsWith('/attachments')) return reply({ attachments });
        if (path.endsWith('/preview')) {
          return reply({
            previewUrl: `https://blob.example/${init.params.path.attId}.png`,
            expiresAt: Math.floor(Date.now() / 1000) + 1800,
          });
        }
        if (path.endsWith('/download')) {
          return reply({ downloadUrl: `https://blob.example/dl/${init.params.path.attId}` });
        }
        throw new Error(`unexpected GET ${path}`);
      },
    };
    const res = (await call(client)) as { data: unknown };
    return res.data;
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

function previewCallsFor(attId: string): number {
  return recorded.calls.filter((c) => c.path.endsWith('/preview') && c.attId === attId).length;
}

function renderTab(): void {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <Suspense fallback={null}>
        <AttachmentsTab workspaceId="w1" calendarId="c1" eventId="e1" />
      </Suspense>
    </QueryClientProvider>,
  );
}

describe('AttachmentsTab previews', () => {
  it('shows thumbnails for previewable rows only and does not load oversized ones', async () => {
    renderTab();
    await screen.findByRole('button', { name: 'gallery.open_preview:a.png' });
    expect(screen.queryByRole('button', { name: 'gallery.open_preview:doc.pdf' })).toBeNull();
    expect(
      screen.getByRole('button', { name: 'gallery.open_preview:big.png' }).getAttribute('title'),
    ).toBe('gallery.too_large_hint');

    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: 'gallery.open_preview:a.png' }).querySelector('img'),
      ).not.toBeNull();
    });
    expect(previewCallsFor('c1')).toBe(0);
  });

  it('opens the gallery at the clicked image and downloads through the download API', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    renderTab();
    await userEvent.click(
      await screen.findByRole('button', { name: 'gallery.open_preview:b.png' }),
    );

    await userEvent.click(await screen.findByRole('button', { name: 'gallery.labels.download' }));

    await waitFor(() => {
      expect(open).toHaveBeenCalledWith('https://blob.example/dl/b1', '_blank');
    });
  });
});
