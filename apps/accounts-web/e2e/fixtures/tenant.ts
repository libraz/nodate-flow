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
 * Grants instance-admin to the given tenant via REST.
 *
 * Three strategies, in order:
 *   1. POST /admin/setup with the tenant's own token. Works on a
 *      pristine instance with no admins yet.
 *   2. If setup returns 409 (admin already exists), log in as the
 *      seeded developer admin (NF_SEED_ADMIN_EMAIL /
 *      NF_SEED_ADMIN_PASSWORD, default admin@example.com /
 *      password123 — see `make seed-flow`) and call POST
 *      /admin/instance-admins on the tenant's behalf using the
 *      seed admin's token.
 *   3. Fallback: try the direct grant with the tenant's token (only
 *      succeeds if the tenant is already admin from a prior run).
 *
 * Returns true if any strategy succeeded.
 */
export async function grantInstanceAdmin(tenant: TestTenant): Promise<boolean> {
  const setupRes = await fetch(`${AUTH_API_URL}/admin/setup`, {
    method: 'POST',
    headers: {
      authorization: `Bearer ${tenant.accessToken}`,
      'Content-Type': 'application/json',
      accept: 'application/json',
    },
  });
  if (setupRes.ok) return true;

  // Setup 409 → an admin already exists. Bootstrap via the seeded
  // developer admin (created by `make seed-flow`).
  const seedEmail = process.env.NF_SEED_ADMIN_EMAIL ?? 'admin@example.com';
  const seedPassword = process.env.NF_SEED_ADMIN_PASSWORD ?? 'password123';
  const seedLoginRes = await fetch(`${AUTH_API_URL}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', accept: 'application/json' },
    body: JSON.stringify({ email: seedEmail, password: seedPassword }),
  });
  if (seedLoginRes.ok) {
    const seedAuth = (await seedLoginRes.json()) as { accessToken: string };
    const grantRes = await fetch(`${AUTH_API_URL}/admin/instance-admins`, {
      method: 'POST',
      headers: {
        authorization: `Bearer ${seedAuth.accessToken}`,
        'Content-Type': 'application/json',
        accept: 'application/json',
      },
      body: JSON.stringify({ userId: tenant.userId }),
    });
    // 409 = tenant is already admin (rerun on a hot DB) → still a success.
    if (grantRes.ok || grantRes.status === 409) return true;
    console.warn(
      `Could not grant admin via seed user: status=${grantRes.status} body=${await grantRes.text()}`,
    );
  }

  // Last resort: tenant might already be admin from a previous run on the
  // same DB. Try the direct grant with their own token.
  const selfGrantRes = await fetch(`${AUTH_API_URL}/admin/instance-admins`, {
    method: 'POST',
    headers: {
      authorization: `Bearer ${tenant.accessToken}`,
      'Content-Type': 'application/json',
      accept: 'application/json',
    },
    body: JSON.stringify({ userId: tenant.userId }),
  });
  if (selfGrantRes.ok || selfGrantRes.status === 409) return true;

  console.warn(
    `Could not grant admin: setup=${setupRes.status} seedLogin=${seedLoginRes.status} selfGrant=${selfGrantRes.status}`,
  );
  return false;
}
