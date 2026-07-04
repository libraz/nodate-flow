import { apiErrorMessage } from './util/api-error.js';

type AuthClientResult = {
  data?: unknown;
  error?: unknown;
  response: Response;
};

type AuthClientLike = {
  post: (path: string, init: { body: Record<string, unknown> }) => Promise<AuthClientResult>;
};

export interface CompleteLoginOptions {
  client: AuthClientLike;
  email: string;
  password: string;
  promptTotp: () => Promise<string>;
}

export interface CompleteLoginResult {
  data?: Record<string, unknown>;
  error?: unknown;
  response?: Response;
}

export function authErrorMessage(error: unknown, fallback: string): string {
  return apiErrorMessage(error, fallback);
}

export async function completeLogin(options: CompleteLoginOptions): Promise<CompleteLoginResult> {
  const first = await options.client.post('/auth/login', {
    body: { email: options.email, password: options.password },
  });
  if (first.error) {
    return { error: first.error, response: first.response };
  }

  const firstData = recordOrEmpty(first.data);
  if (firstData.step !== 'totp_required') {
    return { data: firstData, response: first.response };
  }

  const challengeToken = firstData.challengeToken;
  if (typeof challengeToken !== 'string' || challengeToken.length === 0) {
    return { error: { detail: 'TOTP challenge token missing' }, response: first.response };
  }

  const value = (await options.promptTotp()).trim();
  const body: Record<string, unknown> = { challengeToken };
  if (/^\d{6}$/.test(value)) {
    body.code = value;
  } else {
    body.recoveryCode = value;
  }

  const second = await options.client.post('/auth/login/totp', { body });
  if (second.error) {
    return { error: second.error, response: second.response };
  }
  return { data: recordOrEmpty(second.data), response: second.response };
}

function recordOrEmpty(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : {};
}
