/**
 * Revoking an instance admin must target the user's public_id (`userId`), not
 * the grant row's public_id (`id`). DELETE /admin/instance-admins/{userId}
 * keys off the user, so sending the grant-row id 404s the request. This guards
 * against the id-space mix-up regressing.
 *
 * Intercepted at the fetch boundary with MSW rather than by mocking
 * `lib/sdk`. That distinction is the whole assertion here: a mocked
 * `sdk.DELETE` can only confirm the page passed the *template* path
 * `/admin/instance-admins/{userId}` plus a params object, which says
 * nothing about the URL the client builds from them. Reading
 * `request.url` proves the id actually reached the path segment.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AUTH_API_URL, server, useMockApi } from '@tests/msw/server';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import { HttpResponse, http } from 'msw';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';
import enAdmin from '../../../../../locales/en/admin.json';

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => () => ({ options: {} }),
}));

// Auto-confirm the danger dialog so the revoke request fires.
vi.mock('@nodate-flow/ui/primitives/confirm/action', () => ({
  confirmAction: vi.fn().mockResolvedValue(true),
}));

useMockApi();

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

describe('admins revoke targets the user public_id', () => {
  it('sends userId (not the grant-row id) to DELETE /admin/instance-admins/{userId}', async () => {
    const deleted: string[] = [];
    server.use(
      http.get(`${AUTH_API_URL}/admin/instance-admins`, () =>
        HttpResponse.json({
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
        }),
      ),
      http.delete(`${AUTH_API_URL}/admin/instance-admins/:userId`, ({ request }) => {
        deleted.push(new URL(request.url).pathname);
        return HttpResponse.json({ ok: true });
      }),
    );

    mount();

    const revokeButton = await waitFor(() => {
      const btn = screen.queryByRole('button', { name: enAdmin.admins.revoke });
      if (!btn) throw new Error('revoke button not yet rendered');
      return btn;
    });

    await userEvent.click(revokeButton);

    await waitFor(() => {
      expect(deleted).toHaveLength(1);
    });

    // The user's public_id, not 'grant-row-1', and not an unsubstituted
    // '{userId}' placeholder.
    expect(deleted[0]).toBe('/admin/instance-admins/user-1');

    // Optimistic removal keys off userId, so the row disappears after revoke.
    await waitFor(() => {
      expect(screen.queryByText('Admin One')).toBeNull();
    });
  });
});
