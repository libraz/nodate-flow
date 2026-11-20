/**
 * The expiry section states the share's expiry in words, and clearing it
 * must send `clearExpiresAt` (not a zero or an omitted field, which the
 * server reads as "leave it alone").
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ patch: vi.fn(), toastShow: vi.fn() }));

vi.mock('../../../lib/sdk', () => ({
  sdk: { PATCH: mocks.patch },
  authSdk: { PATCH: mocks.patch },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      [key, ...Object.values(params ?? {})].join(' '),
    i18n: { resolvedLanguage: 'en' },
  }),
}));

import ExpirySection from '../expiry-section';

function renderSection(expiresAt?: number): void {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  const share = expiresAt === undefined ? { id: 'shr-1' } : { id: 'shr-1', expiresAt };
  render(
    <QueryClientProvider client={client}>
      <ExpirySection workspaceId="ws-1" share={share} />
    </QueryClientProvider>,
  );
}

const status = (): string => screen.getByTestId('share-expiry-status').textContent ?? '';

describe('ExpirySection', () => {
  beforeEach(() => {
    mocks.patch.mockReset();
    mocks.toastShow.mockReset();
  });

  it('says "never expires" when no expiry is set', () => {
    renderSection();
    expect(status()).toContain('workspace.public_shares.expiry.status_never');
  });

  it('says when a future expiry will happen', () => {
    renderSection(Math.floor(Date.now() / 1000) + 86_400);
    expect(status()).toContain('workspace.public_shares.expiry.status_expires');
  });

  it('marks a past expiry as expired in text', () => {
    renderSection(1_700_000_000);
    expect(status()).toContain('workspace.public_shares.expiry.status_expired');
  });

  it('clears the expiry with clearExpiresAt', async () => {
    mocks.patch.mockResolvedValue({ data: { id: 'shr-1' }, response: new Response() });
    renderSection(Math.floor(Date.now() / 1000) + 86_400);
    await userEvent.click(screen.getByTestId('share-expiry-edit'));
    await userEvent.click(screen.getByTestId('share-expiry-clear'));
    await waitFor(() => {
      expect(mocks.patch).toHaveBeenCalledTimes(1);
    });
    expect(mocks.patch.mock.calls[0]?.[1]).toMatchObject({
      body: { clearExpiresAt: true },
      params: { path: { wsId: 'ws-1', shareId: 'shr-1' } },
    });
  });
});
