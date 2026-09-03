/**
 * Enrolling in two-factor authentication re-authenticates: both the
 * enroll and the confirm call carry the account password, so a stolen
 * session cannot quietly attach an authenticator app of its own.
 *
 * The password is asserted on the request that goes out, not on the
 * text of the call site, so the check survives the call being
 * rewritten.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: { POST: mocks.post },
  authSdk: { POST: mocks.post },
}));

import { useTotpConfirm, useTotpEnroll } from '../api';

function wrapper(): (props: { children: ReactNode }) => ReactElement {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return function Wrapper({ children }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

beforeEach(() => {
  mocks.post.mockReset();
});

describe('two-factor enrollment re-authentication', () => {
  it('carries the password on the enroll request', async () => {
    mocks.post.mockResolvedValue({
      data: { otpauthUrl: 'otpauth://totp/x', secret: 'S3CR3T' },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    const { result } = renderHook(() => useTotpEnroll(), { wrapper: wrapper() });
    await result.current.mutateAsync('hunter2');

    expect(mocks.post).toHaveBeenCalledWith('/me/totp/enroll', {
      body: { password: 'hunter2' },
    });
  });

  it('carries both the code and the password on the confirm request', async () => {
    mocks.post.mockResolvedValue({
      data: { recoveryCodes: ['aaaa-bbbb'] },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    const { result } = renderHook(() => useTotpConfirm(), { wrapper: wrapper() });
    await result.current.mutateAsync({ code: '123456', password: 'hunter2' });

    expect(mocks.post).toHaveBeenCalledWith('/me/totp/confirm', {
      body: { code: '123456', password: 'hunter2' },
    });
  });

  it('surfaces the refusal code so the form can name the wrong field', async () => {
    mocks.post.mockResolvedValue({
      data: null,
      error: {
        type: 'AUTH.PASSWORD.CURRENT_MISMATCH',
        title: 'Unauthorized',
        detail: 'Current password is incorrect',
        status: 401,
      },
      response: new Response(null, { status: 401 }),
    });

    const { result } = renderHook(() => useTotpEnroll(), { wrapper: wrapper() });
    await expect(result.current.mutateAsync('wrong')).rejects.toMatchObject({
      code: 'AUTH.PASSWORD.CURRENT_MISMATCH',
      httpStatus: 401,
    });
  });

  it('fails the enrollment when the refusal arrived with no error body', async () => {
    // A bodyless 405 or gateway 502 carries no error to read; treating
    // it as a success would show a QR code the server never issued.
    mocks.post.mockResolvedValue({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 502 }),
    });

    const { result } = renderHook(() => useTotpEnroll(), { wrapper: wrapper() });
    await expect(result.current.mutateAsync('hunter2')).rejects.toMatchObject({
      httpStatus: 502,
    });
    await waitFor(() => {
      expect(result.current.isError).toBe(true);
    });
  });
});
