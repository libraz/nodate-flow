/**
 * Tenant fixture for time-web Playwright E2E.
 *
 * Registers via auth-api and creates a workspace for calendar access.
 * The auth-api and time-api must both be running.
 */

import { randomUUID } from 'node:crypto';

import type { BrowserContext } from '@playwright/test';

export const AUTH_API_URL = process.env.NF_AUTH_API_URL ?? 'http://localhost:8082';
export const TIME_API_URL = process.env.NF_TIME_API_URL ?? 'http://localhost:8081';

export interface TestTenant {
  email: string;
  password: string;
  displayName: string;
  accessToken: string;
  refreshToken: string;
  userId: string;
  workspaceId: string;
}

interface RegisterResponse {
  accessToken: string;
  expiresAt: number;
  userId: string;
}

interface WorkspaceResponse {
  id: string;
  slug: string;
}

function extractRefreshToken(res: Response): string {
  const raw = res.headers.get('set-cookie') ?? '';
  const match = raw.match(/nd_rt=([^;]+)/);
  if (!match) {
    throw new Error(`POST /auth/register did not return nd_rt cookie. Set-Cookie: ${raw}`);
  }
  return match[1] as string;
}

export async function createTestTenant(): Promise<TestTenant> {
  const suffix = randomUUID().slice(0, 12);
  const email = `e2e+${suffix}@example.test`;
  const password = 'correct horse battery staple';
  const displayName = `E2E User ${suffix}`;

  const regRes = await fetch(`${AUTH_API_URL}/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', accept: 'application/json' },
    body: JSON.stringify({ email, password, displayName, locale: 'en' }),
  });
  if (!regRes.ok) {
    throw new Error(`POST /auth/register -> ${regRes.status} ${await regRes.text()}`);
  }
  const reg = (await regRes.json()) as RegisterResponse;
  const refreshToken = extractRefreshToken(regRes);

  // Create workspace via auth-api (workspace management lives there)
  const wsRes = await fetch(`${AUTH_API_URL}/workspaces`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${reg.accessToken}`,
    },
    body: JSON.stringify({ slug: `ws-${suffix}`, name: `E2E Workspace ${suffix}` }),
  });
  if (!wsRes.ok) {
    throw new Error(`POST /workspaces -> ${wsRes.status} ${await wsRes.text()}`);
  }
  const ws = (await wsRes.json()) as WorkspaceResponse;

  return {
    email,
    password,
    displayName,
    accessToken: reg.accessToken,
    refreshToken,
    userId: reg.userId,
    workspaceId: ws.id,
  };
}

export async function cleanupTenant(tenant: TestTenant): Promise<void> {
  try {
    await fetch(`${AUTH_API_URL}/auth/logout`, {
      method: 'POST',
      headers: {
        authorization: `Bearer ${tenant.accessToken}`,
        accept: 'application/json',
      },
    });
  } catch {
    // ignore
  }
}

export async function injectAuth(context: BrowserContext, tenant: TestTenant): Promise<void> {
  const page = await context.newPage();
  try {
    await page.goto(AUTH_API_URL, { waitUntil: 'commit', timeout: 5000 }).catch(() => {});

    const result = await page.evaluate(
      async (creds: { email: string; password: string; authUrl: string }) => {
        const res = await fetch(`${creds.authUrl}/auth/login`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          credentials: 'include',
          body: JSON.stringify({ email: creds.email, password: creds.password }),
        });
        return { ok: res.ok, status: res.status };
      },
      { email: tenant.email, password: tenant.password, authUrl: AUTH_API_URL },
    );
    if (!result.ok) {
      throw new Error(`Browser-side login failed: ${result.status}`);
    }
  } finally {
    await page.close();
  }
}
