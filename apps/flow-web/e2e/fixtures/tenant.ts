/**
 * Tenant fixture for Playwright E2E.
 *
 * Mirrors apps/flow-api/tests/helpers/tenant.go: registers a fresh user via
 * POST /auth/register, then creates a workspace + project owned by that
 * user. Each invocation generates a unique email so parallel tests cannot
 * collide.
 *
 * The backend must already be running. Base URL is read from NF_API_URL
 * (default http://localhost:8080).
 *
 * NOTE: this fixture talks directly to the REST API rather than going
 * through the UI for setup. The UI is only exercised by the assertions
 * inside the spec itself, per CLAUDE.md rule 7 (real API integration,
 * no mocks).
 */

import { randomUUID } from 'node:crypto';

import type { BrowserContext } from '@playwright/test';

export const API_BASE_URL = process.env.NF_API_URL ?? 'http://localhost:8080';

export interface TestTenant {
  email: string;
  password: string;
  displayName: string;
  accessToken: string;
  /** Raw nf_rt refresh token extracted from the Set-Cookie header. */
  refreshToken: string;
  userId: string;
  workspaceId: string;
  workspaceSlug: string;
  projectId: string;
  projectSlug: string;
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

interface ProjectResponse {
  id: string;
  slug: string;
}

async function postJson<T>(path: string, body: unknown, bearer?: string): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    accept: 'application/json',
  };
  if (bearer) headers.authorization = `Bearer ${bearer}`;

  const res = await fetch(`${API_BASE_URL}${path}`, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
  });
  const text = await res.text();
  if (!res.ok) {
    throw new Error(`POST ${path} -> ${res.status} ${text}`);
  }
  return text ? (JSON.parse(text) as T) : ({} as T);
}

/**
 * Extracts the nf_rt refresh token value from a Set-Cookie header.
 * The header may contain multiple cookie strings separated by commas
 * or be returned as a single value; we look for the `nf_rt=...`
 * segment.
 */
function extractRefreshToken(res: Response): string {
  const raw = res.headers.get('set-cookie') ?? '';
  const match = raw.match(/nf_rt=([^;]+)/);
  if (!match) {
    throw new Error(`POST /auth/register did not return nf_rt cookie. Set-Cookie: ${raw}`);
  }
  return match[1] as string;
}

/**
 * Creates a fresh tenant via the public REST API and returns the
 * credentials + identifiers needed by the test.
 */
export async function createTestTenant(): Promise<TestTenant> {
  const suffix = randomUUID().slice(0, 12);
  const email = `e2e+${suffix}@example.test`;
  const password = 'correct horse battery staple';
  const displayName = `E2E User ${suffix}`;

  // Register with raw fetch so we can capture the nf_rt Set-Cookie.
  const regRes = await fetch(`${API_BASE_URL}/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', accept: 'application/json' },
    body: JSON.stringify({ email, password, displayName, locale: 'en' }),
  });
  if (!regRes.ok) {
    throw new Error(`POST /auth/register -> ${regRes.status} ${await regRes.text()}`);
  }
  const reg = (await regRes.json()) as RegisterResponse;
  const refreshToken = extractRefreshToken(regRes);

  const workspaceSlug = `ws-${suffix}`;
  const ws = await postJson<WorkspaceResponse>(
    '/workspaces',
    { slug: workspaceSlug, name: `E2E Workspace ${suffix}` },
    reg.accessToken,
  );

  const projectSlug = `prj-${suffix}`;
  const prj = await postJson<ProjectResponse>(
    `/workspaces/${ws.id}/projects`,
    { slug: projectSlug, name: `E2E Project ${suffix}` },
    reg.accessToken,
  );

  return {
    email,
    password,
    displayName,
    accessToken: reg.accessToken,
    refreshToken,
    userId: reg.userId,
    workspaceId: ws.id,
    workspaceSlug: ws.slug,
    projectId: prj.id,
    projectSlug: prj.slug,
  };
}

/**
 * Best-effort logout. Failures are swallowed because cleanup must not
 * mask the underlying test failure.
 */
export async function cleanupTenant(tenant: TestTenant): Promise<void> {
  try {
    await fetch(`${API_BASE_URL}/auth/logout`, {
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
 * Creates a task inside the tenant's default project via REST and
 * returns its title. Used by the smoke spec to seed a row whose title
 * the UI must then render.
 */
export async function createTask(
  tenant: TestTenant,
  title: string,
): Promise<{ id: string; title: string }> {
  const res = await postJson<{ id: string; title: string }>(
    `/workspaces/${tenant.workspaceId}/projects/${tenant.projectId}/tasks`,
    { title },
    tenant.accessToken,
  );
  return res;
}

/**
 * Injects the tenant's nf_rt refresh cookie into the browser context so
 * the app's bootstrap flow (POST /auth/refresh) succeeds on the next
 * navigation. This replaces the broken localStorage approach -- the auth
 * store is in-memory only and the app re-establishes sessions exclusively
 * via the httpOnly nf_rt cookie.
 *
 * Must be called BEFORE page.goto().
 */
export async function injectAuth(context: BrowserContext, tenant: TestTenant): Promise<void> {
  const url = new URL(API_BASE_URL);
  await context.addCookies([
    {
      name: 'nf_rt',
      value: tenant.refreshToken,
      domain: url.hostname,
      path: '/auth',
      httpOnly: true,
      secure: url.protocol === 'https:',
      sameSite: url.protocol === 'https:' ? 'None' : 'Lax',
    },
  ]);
}
