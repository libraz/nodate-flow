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
export const AUTH_API_URL = process.env.NF_AUTH_API_URL ?? 'http://localhost:8082';

export interface TestTenant {
  email: string;
  password: string;
  displayName: string;
  accessToken: string;
  /** Raw nd_rt refresh token extracted from the Set-Cookie header. */
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

async function postJson<T>(
  baseUrl: string,
  path: string,
  body: unknown,
  bearer?: string,
): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    accept: 'application/json',
  };
  if (bearer) headers.authorization = `Bearer ${bearer}`;

  const maxRetries = 5;
  for (let attempt = 0; attempt < maxRetries; attempt++) {
    const res = await fetch(`${baseUrl}${path}`, {
      method: 'POST',
      headers,
      body: JSON.stringify(body),
    });
    const text = await res.text();
    if ((res.status === 429 || res.status === 500) && attempt < maxRetries - 1) {
      // Wait for rate limit window to expire or server to recover, then retry
      await new Promise((r) => setTimeout(r, 5000 * (attempt + 1)));
      continue;
    }
    if (!res.ok) {
      throw new Error(`POST ${path} -> ${res.status} ${text}`);
    }
    return text ? (JSON.parse(text) as T) : ({} as T);
  }
  throw new Error(`POST ${path} -> exhausted retries`);
}

/**
 * Extracts the nd_rt refresh token value from a Set-Cookie header.
 * The header may contain multiple cookie strings separated by commas
 * or be returned as a single value; we look for the `nd_rt=...`
 * segment.
 */
function extractRefreshToken(res: Response): string {
  const raw = res.headers.get('set-cookie') ?? '';
  const match = raw.match(/nd_rt=([^;]+)/);
  if (!match) {
    throw new Error(`POST /auth/register did not return nd_rt cookie. Set-Cookie: ${raw}`);
  }
  return match[1] as string;
}

/** Options accepted by {@link createTestTenant}. */
export interface CreateTestTenantOptions {
  /**
   * Locale persisted in the user profile. The web app's auth bootstrap
   * calls `setLanguage(user.locale)` after login, so passing 'ja' here
   * makes every subsequent UI render in Japanese without further setup.
   * Defaults to 'en'.
   */
  locale?: 'en' | 'ja';
}

/**
 * Creates a fresh tenant via the public REST API and returns the
 * credentials + identifiers needed by the test.
 */
export async function createTestTenant(opts: CreateTestTenantOptions = {}): Promise<TestTenant> {
  const locale = opts.locale ?? 'en';
  const suffix = randomUUID().slice(0, 12);
  const email = `e2e+${suffix}@example.test`;
  const password = 'correct horse battery staple';
  const displayName = `E2E User ${suffix}`;

  // Register with raw fetch so we can capture the nd_rt Set-Cookie.
  // Auth endpoints live on auth-api (port 8082), not flow-api (port 8080).
  async function register(): Promise<Response> {
    for (let attempt = 0; attempt < 5; attempt++) {
      const res = await fetch(`${AUTH_API_URL}/auth/register`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', accept: 'application/json' },
        body: JSON.stringify({ email, password, displayName, locale }),
      });
      if (res.status === 429 && attempt < 4) {
        await new Promise((r) => setTimeout(r, 5000 * (attempt + 1)));
        continue;
      }
      return res;
    }
    throw new Error('POST /auth/register -> exhausted retries');
  }
  const regRes = await register();
  if (!regRes.ok) {
    throw new Error(`POST /auth/register -> ${regRes.status} ${await regRes.text()}`);
  }
  const reg = (await regRes.json()) as RegisterResponse;
  const refreshToken = extractRefreshToken(regRes);

  const workspaceSlug = `ws-${suffix}`;
  // Workspaces live on auth-api
  const ws = await postJson<WorkspaceResponse>(
    AUTH_API_URL,
    '/workspaces',
    { slug: workspaceSlug, name: `E2E Workspace ${suffix}` },
    reg.accessToken,
  );

  // Projects live on flow-api
  const projectSlug = `prj-${suffix}`;
  const prj = await postJson<ProjectResponse>(
    API_BASE_URL,
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
 * Creates a task inside the tenant's default project via REST and
 * returns its title. Used by the smoke spec to seed a row whose title
 * the UI must then render.
 */
export async function createTask(
  tenant: TestTenant,
  title: string,
): Promise<{ id: string; title: string }> {
  const res = await postJson<{ id: string; title: string }>(
    API_BASE_URL,
    '/tasks',
    { title, projectId: tenant.projectId },
    tenant.accessToken,
  );
  return res;
}

/**
 * Fetches the tenant's calendar list. The GET triggers the lazy
 * auto-creation of the personal + system calendars, so after this call
 * the workspace is guaranteed to have at least one writable personal
 * calendar the user owns.
 *
 * Returns the first calendar whose role is `owner` so the caller can
 * POST events into it. Throws if the list is empty or the call fails —
 * both are regressions we want to surface loudly rather than mask.
 */
export async function ensurePersonalCalendar(
  tenant: TestTenant,
): Promise<{ id: string; name: string; role: string }> {
  const res = await fetch(`${API_BASE_URL}/workspaces/${tenant.workspaceId}/calendars`, {
    method: 'GET',
    headers: {
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
  });
  if (!res.ok) {
    throw new Error(`GET /workspaces/{wsId}/calendars -> ${res.status} ${await res.text()}`);
  }
  const body = (await res.json()) as {
    calendars: Array<{ id: string; name: string; role: string }>;
  };
  const owned = body.calendars.find((c) => c.role === 'owner');
  if (!owned) {
    throw new Error(
      `no owner-role calendar auto-created for ws=${tenant.workspaceId} (got ${body.calendars.length} calendars)`,
    );
  }
  return owned;
}

/** Args for {@link createCalendarEvent}. Unix times are seconds (UTC). */
export interface CreateCalendarEventArgs {
  title: string;
  startAt: number;
  endAt: number;
  kind?: 'event' | 'block' | 'free' | 'milestone';
  allDay?: boolean;
  timezone?: string;
}

/**
 * Seeds a calendar event directly via REST. Used by edit/delete tests
 * that need a pre-existing row to click on without going through the
 * create UI path first.
 */
export async function createCalendarEvent(
  tenant: TestTenant,
  calendarId: string,
  args: CreateCalendarEventArgs,
): Promise<{ id: string; title: string; kind: string }> {
  const body = {
    kind: args.kind ?? 'event',
    title: args.title,
    startAt: args.startAt,
    endAt: args.endAt,
    allDay: args.allDay ?? false,
    timezone: args.timezone ?? 'UTC',
  };
  const res = await fetch(
    `${API_BASE_URL}/workspaces/${tenant.workspaceId}/calendars/${calendarId}/events`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        accept: 'application/json',
        authorization: `Bearer ${tenant.accessToken}`,
      },
      body: JSON.stringify(body),
    },
  );
  if (!res.ok) {
    throw new Error(`POST /calendars/{calId}/events -> ${res.status} ${await res.text()}`);
  }
  return (await res.json()) as { id: string; title: string; kind: string };
}

/**
 * Injects the tenant's nd_rt refresh cookie into the browser context so
 * the app's bootstrap flow (POST /auth/refresh) succeeds on the next
 * navigation. This replaces the broken localStorage approach -- the auth
 * store is in-memory only and the app re-establishes sessions exclusively
 * via the httpOnly nd_rt cookie.
 *
 * Must be called BEFORE page.goto().
 */
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
        const body = await res.text();
        return { ok: res.ok, status: res.status, body };
      },
      { email: tenant.email, password: tenant.password, authUrl: AUTH_API_URL },
    );
    if (!result.ok) {
      throw new Error(`Browser-side login failed: ${result.status} ${result.body}`);
    }
  } finally {
    await page.close();
  }
}
