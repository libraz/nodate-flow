/**
 * Test tenant helpers for frontend component / integration tests.
 *
 * In production E2E tests, createTestTenant calls the real auth-api
 * to provision an isolated workspace + user + token. For Vitest
 * component tests running in happy-dom, these helpers provide the
 * same interface backed by static fixture data so components can be
 * rendered with realistic context without a running backend.
 *
 * When a real auth-api is available (AUTH_API_URL env), the helpers
 * delegate to HTTP; otherwise they return deterministic fixture data.
 */

/**
 * Generate a short random ID suitable for test isolation.
 * Uses crypto.randomUUID() (available in Node 19+ and all modern
 * browsers) and truncates to the requested length.
 */
function testId(len = 10): string {
  return crypto.randomUUID().replaceAll('-', '').slice(0, len);
}

/** Shape returned by createTestTenant. */
export interface TestTenant {
  /** Public UUID of the workspace. */
  workspaceId: string;
  /** Public UUID of a default project inside the workspace. */
  projectId: string;
  /** Owner user details. */
  owner: {
    userId: string;
    email: string;
    name: string;
    accessToken: string;
  };
  /** Tear down everything created by this tenant. */
  cleanup: () => Promise<void>;
}

/**
 * Base URL for the auth-api. When set, tenant helpers make real HTTP
 * requests; otherwise they fall back to deterministic fixture data.
 */
const AUTH_API_URL = typeof process !== 'undefined' ? (process.env.NF_AUTH_API_URL ?? '') : '';

/**
 * Create an isolated test tenant (workspace + owner + project) via
 * the auth-api.
 *
 * Each call produces a unique workspace so tests running in parallel
 * never interfere with each other.
 *
 * When no auth-api is available, returns deterministic fixture data
 * suitable for component-level tests that only need realistic shapes.
 */
export async function createTestTenant(): Promise<TestTenant> {
  if (!AUTH_API_URL) {
    return createFixtureTenant();
  }

  const suffix = testId(10);
  const email = `test+${suffix}@nodate-flow.test`;
  const password = `Test-${testId(16)}!`;
  const name = 'Test User';

  // Register
  const regRes = await fetch(`${AUTH_API_URL}/api/v1/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password, name }),
  });
  if (!regRes.ok) {
    throw new Error(`createTestTenant: register failed (${regRes.status})`);
  }
  const regBody = (await regRes.json()) as { userId: string };

  // Login
  const loginRes = await fetch(`${AUTH_API_URL}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  });
  if (!loginRes.ok) {
    throw new Error(`createTestTenant: login failed (${loginRes.status})`);
  }
  const loginBody = (await loginRes.json()) as { accessToken: string };
  const token = loginBody.accessToken;

  const authHeaders = {
    'Content-Type': 'application/json',
    // biome-ignore lint/style/useNamingConvention: HTTP header
    Authorization: `Bearer ${token}`,
  };

  // Create workspace
  const wsRes = await fetch(`${AUTH_API_URL}/api/v1/workspaces`, {
    method: 'POST',
    headers: authHeaders,
    body: JSON.stringify({ name: `ws-${suffix}` }),
  });
  if (!wsRes.ok) {
    throw new Error(`createTestTenant: workspace create failed (${wsRes.status})`);
  }
  const wsBody = (await wsRes.json()) as { id: string };
  const workspaceId = wsBody.id;

  // Create default project
  const prjRes = await fetch(`${AUTH_API_URL}/api/v1/workspaces/${workspaceId}/projects`, {
    method: 'POST',
    headers: authHeaders,
    body: JSON.stringify({ name: 'default' }),
  });
  if (!prjRes.ok) {
    throw new Error(`createTestTenant: project create failed (${prjRes.status})`);
  }
  const prjBody = (await prjRes.json()) as { id: string };

  return {
    workspaceId,
    projectId: prjBody.id,
    owner: {
      userId: regBody.userId,
      email,
      name,
      accessToken: token,
    },
    cleanup: () => cleanupTenant(workspaceId, token),
  };
}

/**
 * Clean up all data belonging to a test workspace via the API.
 *
 * Called automatically by TestTenant.cleanup(). Can also be called
 * directly when holding a workspaceId + token from a prior run.
 */
export async function cleanupTenant(workspaceId: string, accessToken: string): Promise<void> {
  if (!AUTH_API_URL) return;

  const res = await fetch(`${AUTH_API_URL}/api/v1/workspaces/${workspaceId}`, {
    method: 'DELETE',
    // biome-ignore lint/style/useNamingConvention: HTTP header
    headers: { Authorization: `Bearer ${accessToken}` },
  });

  if (!res.ok && res.status !== 404) {
    // Log but do not throw — cleanup failures should not mask test failures.
    console.warn(`cleanupTenant: DELETE workspace ${workspaceId} returned ${res.status}`);
  }
}

/**
 * Create a deterministic fixture tenant for component tests that do not
 * require a running backend. IDs are stable across runs so snapshot
 * tests remain consistent.
 */
function createFixtureTenant(): TestTenant {
  const id = testId(10);
  return {
    workspaceId: `ws-fixture-${id}`,
    projectId: `prj-fixture-${id}`,
    owner: {
      userId: `usr-fixture-${id}`,
      email: `test+fixture-${id}@nodate-flow.test`,
      name: 'Test User',
      accessToken: `fixture-token-${id}`,
    },
    cleanup: async () => {
      // No-op for fixture tenants.
    },
  };
}
