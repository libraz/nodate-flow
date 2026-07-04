import { describe, expect, it, vi } from 'vitest';

import { authErrorMessage, completeLogin } from '../src/auth-login.js';

describe('completeLogin', () => {
  it('completes a single-factor login without prompting for TOTP', async () => {
    const promptTotp = vi.fn();
    const response = new Response('{}', { status: 200 });
    const client = {
      POST: vi.fn(async () => ({
        data: { step: 'complete', accessToken: 'access-1' },
        response,
      })),
    };

    const result = await completeLogin({
      client,
      email: 'me@example.com',
      password: 'pw',
      promptTotp,
    });

    expect(result.data?.accessToken).toBe('access-1');
    expect(result.response).toBe(response);
    expect(promptTotp).not.toHaveBeenCalled();
    expect(client.POST).toHaveBeenCalledWith('/auth/login', {
      body: { email: 'me@example.com', password: 'pw' },
    });
  });

  it('submits a six-digit TOTP code when the first login leg requires it', async () => {
    const finalResponse = new Response('{}', { status: 200 });
    const client = {
      POST: vi
        .fn()
        .mockResolvedValueOnce({
          data: { step: 'totp_required', challengeToken: 'challenge-1' },
          response: new Response('{}', { status: 200 }),
        })
        .mockResolvedValueOnce({
          data: { step: 'complete', accessToken: 'access-2' },
          response: finalResponse,
        }),
    };

    const result = await completeLogin({
      client,
      email: 'me@example.com',
      password: 'pw',
      promptTotp: async () => '123456',
    });

    expect(result.data?.accessToken).toBe('access-2');
    expect(result.response).toBe(finalResponse);
    expect(client.POST).toHaveBeenNthCalledWith(2, '/auth/login/totp', {
      body: { challengeToken: 'challenge-1', code: '123456' },
    });
  });

  it('submits a recovery code when the TOTP prompt value is not six digits', async () => {
    const client = {
      POST: vi
        .fn()
        .mockResolvedValueOnce({
          data: { step: 'totp_required', challengeToken: 'challenge-1' },
          response: new Response('{}', { status: 200 }),
        })
        .mockResolvedValueOnce({
          data: { step: 'complete', accessToken: 'access-3' },
          response: new Response('{}', { status: 200 }),
        }),
    };

    await completeLogin({
      client,
      email: 'me@example.com',
      password: 'pw',
      promptTotp: async () => 'ABCD-1234-EFGH',
    });

    expect(client.POST).toHaveBeenNthCalledWith(2, '/auth/login/totp', {
      body: { challengeToken: 'challenge-1', recoveryCode: 'ABCD-1234-EFGH' },
    });
  });

  it('returns a clear error when the challenge token is missing', async () => {
    const response = new Response('{}', { status: 200 });
    const client = {
      POST: vi.fn(async () => ({
        data: { step: 'totp_required' },
        response,
      })),
    };

    const result = await completeLogin({
      client,
      email: 'me@example.com',
      password: 'pw',
      promptTotp: async () => '123456',
    });

    expect(authErrorMessage(result.error, 'Login failed')).toBe('TOTP challenge token missing');
    expect(client.POST).toHaveBeenCalledTimes(1);
  });
});
