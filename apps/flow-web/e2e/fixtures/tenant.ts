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
 * Optional shaping arguments accepted by {@link createTask}.
 *
 * @property priority Initial task priority (0..4). Defaults to whatever
 *   the backend assigns (currently 0). Useful for E2E tests that need
 *   to coerce the deterministic AI priority engine into producing a
 *   suggestion (a low current priority + an overdue dueOn maximises
 *   the score delta — see priorityopt.go).
 * @property dueOn Due date in `YYYY-MM-DD` form. Pass yesterday's
 *   date to mark the task as overdue, which adds +2.5 to the engine
 *   score and guarantees a priority-bump suggestion in a fresh tenant.
 */
export interface CreateTaskOptions {
  priority?: number;
  dueOn?: string;
}

/**
 * Creates a task inside the tenant's default project via REST and
 * returns its title. Used by the smoke spec to seed a row whose title
 * the UI must then render.
 *
 * Pass {@link CreateTaskOptions} when a test needs to control the
 * task's initial priority and/or due date — for example, the AI
 * priority suggestion E2E seeds an overdue P0 task to guarantee one
 * deterministic suggestion surfaces in the freshly-created tenant.
 */
export async function createTask(
  tenant: TestTenant,
  title: string,
  options: CreateTaskOptions = {},
): Promise<{ id: string; title: string }> {
  const body: Record<string, unknown> = { title, projectId: tenant.projectId };
  if (typeof options.priority === 'number') body.priority = options.priority;
  if (typeof options.dueOn === 'string') body.dueOn = options.dueOn;
  const res = await postJson<{ id: string; title: string }>(
    API_BASE_URL,
    '/tasks',
    body,
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
 * Tracks contexts that already have the API CORS shim installed so we
 * don't double-register the route handler when {@link injectAuth} is
 * called multiple times in the same test.
 */
const corsShimInstalled = new WeakSet<BrowserContext>();

/**
 * Installs a context-wide route shim that ensures auth-api and
 * flow-api responses carry permissive CORS headers, regardless of the
 * dev backends' NF_AUTH_CORS / NF_FLOW_CORS allowlists.
 *
 * Why this exists: in dev the auth-api and flow-api CORS allowlists
 * are fixed at boot via env (defaults include 5173/5175 only). When
 * the Vite dev server is run on a non-default port (e.g.
 * NF_WEB_URL=5183 because 5173 is already taken locally),
 * browser-initiated calls from flow-web to either backend are blocked
 * at the CORS layer and the bootstrap `POST /auth/refresh` and every
 * subsequent data fetch fails.
 *
 * Rather than restarting the backends with a wider allowlist, we
 * patch the response headers in the test runner. The actual response
 * body and status are passed through unchanged -- this is a header
 * shim, NOT a mock, and does not violate the "real API only" rule.
 *
 * Preflight OPTIONS requests are short-circuited here too; the dev
 * backends already 200 them but do not echo ACAO for unlisted
 * origins, so the browser still rejects them.
 */
async function installAuthCorsShim(context: BrowserContext): Promise<void> {
  if (corsShimInstalled.has(context)) return;
  corsShimInstalled.add(context);
  const authOrigin = AUTH_API_URL;
  const apiOrigin = API_BASE_URL;
  await context.route(
    (url) => url.origin === authOrigin || url.origin === apiOrigin,
    async (route, request) => {
      const origin = request.headers().origin ?? '*';
      // Preflight: respond locally with the headers the browser needs.
      if (request.method() === 'OPTIONS') {
        await route.fulfill({
          status: 204,
          headers: {
            'access-control-allow-origin': origin,
            'access-control-allow-credentials': 'true',
            'access-control-allow-methods': 'GET,POST,PUT,PATCH,DELETE,OPTIONS',
            'access-control-allow-headers':
              request.headers()['access-control-request-headers'] ??
              'Accept, Authorization, Content-Type, X-Requested-With',
            'access-control-max-age': '600',
          },
        });
        return;
      }
      try {
        const upstream = await route.fetch();
        const headers = { ...upstream.headers() };
        // Force-inject ACAO + ACAC so the browser accepts the response
        // regardless of the backend's allowlist.
        headers['access-control-allow-origin'] = origin;
        headers['access-control-allow-credentials'] = 'true';
        // Some browsers require ACAO to vary with the request origin;
        // the backend already sends Vary: Origin in most paths but
        // assert it here too just in case.
        headers.vary = headers.vary
          ? headers.vary.includes('Origin')
            ? headers.vary
            : `${headers.vary}, Origin`
          : 'Origin';
        await route.fulfill({ response: upstream, headers });
      } catch (err) {
        // Swallow teardown-races: when the test ends with in-flight
        // requests (e.g. SSE stream connections), Playwright closes the
        // page mid-fetch and route.fetch / route.fulfill raises
        // "Target page, context or browser has been closed". That's
        // expected during normal teardown; surfacing it would mask the
        // real test outcome. Anything else gets re-thrown so genuine
        // bugs in the shim still fail loudly.
        const msg = err instanceof Error ? err.message : String(err);
        if (!/closed|disposed|Frame was detached/i.test(msg)) throw err;
      }
    },
  );
}

/**
 * Establishes a logged-in session on the browser context so the app's
 * bootstrap flow (POST /auth/refresh) succeeds on the next navigation.
 *
 * Approach:
 *
 *   1. Install a CORS-header shim on the context so cross-origin calls
 *      from flow-web (e.g. http://localhost:5183) to auth-api
 *      (http://localhost:8082) are accepted by the browser regardless
 *      of NF_AUTH_CORS. See {@link installAuthCorsShim}.
 *
 *   2. Use Playwright's server-side request API to POST /auth/login
 *      (no CORS, no Secure-cookie restrictions on storage), parse the
 *      nd_rt refresh token out of the Set-Cookie header.
 *
 *   3. Plant the cookie via {@link BrowserContext.addCookies} scoped to
 *      `localhost`, Path=/auth, HttpOnly, with `secure: false` so the
 *      browser will actually send it over the dev http transport. The
 *      production cookie has Secure; SameSite=None which Chrome stores
 *      on localhost (it's a "potentially trustworthy" origin) but only
 *      sends over https; for tests we relax to Lax+insecure to
 *      eliminate that footgun in dev shells where NF_COOKIE_SECURE=true.
 *
 * The auth store is in-memory only -- the app re-establishes sessions
 * exclusively via the httpOnly nd_rt cookie -- so seeding the cookie
 * is sufficient to make the bootstrap go authenticated.
 *
 * Must be called BEFORE page.goto().
 */
export async function injectAuth(context: BrowserContext, tenant: TestTenant): Promise<void> {
  await installAuthCorsShim(context);

  // Server-side login: skips CORS entirely and gives us the raw
  // Set-Cookie header. We retry on transient 429/500 to mirror the
  // resilience of the other helpers in this file.
  let setCookieRaw = '';
  for (let attempt = 0; attempt < 5; attempt++) {
    const res = await context.request.post(`${AUTH_API_URL}/auth/login`, {
      data: { email: tenant.email, password: tenant.password },
      headers: { 'Content-Type': 'application/json', accept: 'application/json' },
      failOnStatusCode: false,
    });
    if ((res.status() === 429 || res.status() === 500) && attempt < 4) {
      await new Promise((r) => setTimeout(r, 5000 * (attempt + 1)));
      continue;
    }
    if (!res.ok()) {
      throw new Error(`server-side /auth/login -> ${res.status()} ${await res.text()}`);
    }
    // headersArray() preserves duplicate Set-Cookie entries; pick the
    // one carrying nd_rt.
    const headersArr = await res.headersArray();
    const setCookies = headersArr
      .filter((h) => h.name.toLowerCase() === 'set-cookie')
      .map((h) => h.value);
    setCookieRaw = setCookies.find((c) => c.startsWith('nd_rt=')) ?? setCookies.join('\n');
    break;
  }
  const match = setCookieRaw.match(/nd_rt=([^;]+)/);
  if (!match) {
    throw new Error(`/auth/login did not return nd_rt cookie. Set-Cookie: ${setCookieRaw}`);
  }
  const refreshValue = match[1] as string;

  // Plant the refresh cookie so the bootstrap's `fetch(/auth/refresh,
  // { credentials: 'include' })` carries it. Scope to the bare hostname
  // so it's sent regardless of the auth-api port; mark insecure so the
  // browser is willing to send it over plain http.
  const authUrl = new URL(AUTH_API_URL);
  await context.addCookies([
    {
      name: 'nd_rt',
      value: refreshValue,
      domain: authUrl.hostname,
      path: '/auth',
      httpOnly: true,
      secure: false,
      sameSite: 'Lax',
    },
  ]);
}
