/**
 * Tenant fixture for accounts-web Playwright E2E.
 *
 * Registers a fresh user via POST /auth/register on the auth-api
 * backend. Each invocation generates a unique email so parallel
 * tests cannot collide.
 *
 * The auth-api must already be running. Base URL is read from
 * NF_AUTH_API_URL (default http://localhost:8082).
 */

import { randomUUID } from 'node:crypto';

import type { BrowserContext } from '@playwright/test';

export const AUTH_API_URL = process.env.NF_AUTH_API_URL ?? 'http://localhost:8082';

export interface TestTenant {
  email: string;
  password: string;
  displayName: string;
  accessToken: string;
  refreshToken: string;
  userId: string;
}

interface RegisterResponse {
  accessToken: string;
  expiresAt: number;
  userId: string;
}

function extractRefreshToken(res: Response): string {
  const raw = res.headers.get('set-cookie') ?? '';
  const match = raw.match(/nd_rt=([^;]+)/);
  if (!match) {
    throw new Error(`POST /auth/register did not return nd_rt cookie. Set-Cookie: ${raw}`);
  }
  return match[1] as string;
}

async function registerWithRetry(
  email: string,
  password: string,
  displayName: string,
): Promise<Response> {
  const maxRetries = 6;
  for (let attempt = 0; attempt < maxRetries; attempt++) {
    const res = await fetch(`${AUTH_API_URL}/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', accept: 'application/json' },
      body: JSON.stringify({ email, password, displayName, locale: 'en' }),
    });
    if (res.ok) return res;
    if (res.status === 429 && attempt < maxRetries - 1) {
      // Respect Retry-After header from the server, fall back to 30s
      const retryAfter = res.headers.get('retry-after');
      const delay = retryAfter ? (Number.parseInt(retryAfter, 10) + 2) * 1000 : 30_000;
      await new Promise((r) => setTimeout(r, delay));
      continue;
    }
    throw new Error(`POST /auth/register -> ${res.status} ${await res.text()}`);
  }
  throw new Error('registerWithRetry: unreachable');
}

export async function createTestTenant(): Promise<TestTenant> {
  // Stagger requests to avoid hitting the rate limiter when parallel
  // workers all call createTestTenant simultaneously.
  await new Promise((r) => setTimeout(r, Math.random() * 1500));

  const suffix = randomUUID().slice(0, 12);
  const email = `e2e+${suffix}@example.test`;
  const password = 'correct horse battery staple';
  const displayName = `E2E User ${suffix}`;

  const regRes = await registerWithRetry(email, password, displayName);
  const reg = (await regRes.json()) as RegisterResponse;
  const refreshToken = extractRefreshToken(regRes);

  return {
    email,
    password,
    displayName,
    accessToken: reg.accessToken,
    refreshToken,
    userId: reg.userId,
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

/**
 * Establishes the auth session by logging in from within the browser
 * context. This is more reliable than manually injecting cookies
 * because the server's Set-Cookie response (with its exact Secure,
 * SameSite, Path flags) is handled natively by the browser engine.
 *
 * Must be called BEFORE navigating to authenticated routes.
 */
export async function injectAuth(context: BrowserContext, tenant: TestTenant): Promise<void> {
  const page = await context.newPage();
  try {
    // Navigate to the auth-api origin so the login fetch is same-origin
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

/**
 * Grants instance-admin to the given tenant via REST.
 * Uses the bootstrap endpoint that makes the first user admin if none exist,
 * or grants directly if the tenant is already admin.
 */
/**
 * Attempts to grant instance-admin to the given tenant via REST.
 * Returns true if successful, false if admin already exists and
 * we can't self-promote (409 from setup + 403 from direct grant).
 */
export async function grantInstanceAdmin(tenant: TestTenant): Promise<boolean> {
  const res = await fetch(`${AUTH_API_URL}/admin/setup`, {
    method: 'POST',
    headers: {
      authorization: `Bearer ${tenant.accessToken}`,
      'Content-Type': 'application/json',
      accept: 'application/json',
    },
  });
  if (res.ok) return true;

  // Setup returned 409 (admin exists). Try direct grant (needs existing admin).
  const grantRes = await fetch(`${AUTH_API_URL}/admin/instance-admins`, {
    method: 'POST',
    headers: {
      authorization: `Bearer ${tenant.accessToken}`,
      'Content-Type': 'application/json',
      accept: 'application/json',
    },
    body: JSON.stringify({ userId: tenant.userId }),
  });
  if (grantRes.ok) return true;

  console.warn(`Could not grant admin: setup=${res.status} grant=${grantRes.status}`);
  return false;
}
