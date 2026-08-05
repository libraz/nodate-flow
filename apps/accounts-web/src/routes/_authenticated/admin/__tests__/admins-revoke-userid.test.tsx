/**
 * Revoking an instance admin must target the user's public_id (`userId`), not
 * the grant row's public_id (`id`). DELETE /admin/instance-admins/{userId}
 * keys off the user, so sending the grant-row id 404s the request. This guards
 * against the id-space mix-up regressing.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import enAdmin from '../../../../../locales/en/admin.json';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  delete: vi.fn(),
}));

vi.mock('../../../../lib/sdk', () => ({
  sdk: {
    GET: sdkMocks.get,
    POST: sdkMocks.post,
    DELETE: sdkMocks.delete,
  },
}));

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => () => ({ options: {} }),
}));

// Auto-confirm the danger dialog so the revoke request fires.
vi.mock('@nodate-flow/ui/primitives/confirm/action', () => ({
  confirmAction: vi.fn().mockResolvedValue(true),
}));

const { AdminsPage } = await import('../admins');

function buildI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'admin',
      ns: ['admin'],
      resources: { en: { admin: enAdmin } },
      interpolation: { escapeValue: false },
      react: { useSuspense: false },
    });
  return instance;
}

function mount(): void {
  const testI18n = buildI18n();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });

  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={qc}>
        <I18nextProvider i18n={testI18n}>{children}</I18nextProvider>
      </QueryClientProvider>
    );
  }

  render(<AdminsPage />, { wrapper: Wrapper });
}

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.post.mockReset();
  sdkMocks.delete.mockReset();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('admins revoke targets the user public_id', () => {
  it('sends userId (not the grant-row id) to DELETE /admin/instance-admins/{userId}', async () => {
    sdkMocks.get.mockResolvedValueOnce({
      data: {
        items: [
          {
            id: 'grant-row-1',
            userId: 'user-1',
            email: 'admin@example.test',
            displayName: 'Admin One',
            grantedAt: 1700000000,
          },
        ],
        total: 1,
      },
    });
    sdkMocks.delete.mockResolvedValueOnce({ data: { ok: true } });

    mount();

    const revokeButton = await waitFor(() => {
      const btn = screen.queryByRole('button', { name: enAdmin.admins.revoke });
      if (!btn) throw new Error('revoke button not yet rendered');
      return btn;
    });

    await userEvent.click(revokeButton);

    await waitFor(() => {
      expect(sdkMocks.delete).toHaveBeenCalledTimes(1);
    });

    expect(sdkMocks.delete).toHaveBeenCalledWith('/admin/instance-admins/{userId}', {
      params: { path: { userId: 'user-1' } },
    });

    // Optimistic removal keys off userId, so the row disappears after revoke.
    await waitFor(() => {
      expect(screen.queryByText('Admin One')).toBeNull();
    });
  });
});
