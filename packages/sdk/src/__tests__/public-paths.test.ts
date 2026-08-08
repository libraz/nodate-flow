/**
 * The refresh middleware's idea of which operations are public has to
 * match the API's.
 *
 * The list in refresh.ts is hand-written, and the cost of it falling
 * behind is invisible: a new unauthenticated endpoint would work, and
 * merely make every anonymous visitor wait on a refused round trip to
 * auth-api first, spending a rate-limit budget that signed-in colleagues
 * behind the same address need. So rather than restate the list here,
 * this test reads the committed spec — where an operation is public
 * exactly when it declares no security requirement — and drives every
 * such path through the middleware.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The committed document is imported rather than read off disk: the SDK
// ships to browser bundles and deliberately carries no Node types, so a
// `node:fs` read here would mean adding them for the whole package and
// letting shipped code reach for `Buffer` without anything complaining.
import openapi from '../../openapi.json';
import { createAuthRequestMiddleware, createTokenRefresher } from '../refresh';

interface Spec {
  paths: Record<string, Record<string, { security?: unknown[] } | unknown>>;
}

const spec = openapi as unknown as Spec;

const HTTP_METHODS = ['get', 'post', 'put', 'patch', 'delete'] as const;

/**
 * Every (method, path) in the spec, split by whether it declares a
 * security requirement. Path templates are filled with a placeholder so
 * they can be turned into real URLs.
 */
function operations(): { publicPaths: string[]; privatePaths: string[] } {
  const publicPaths = new Set<string>();
  const privatePaths = new Set<string>();
  for (const [path, item] of Object.entries(spec.paths)) {
    for (const method of HTTP_METHODS) {
      const op = item[method] as { security?: unknown[] } | undefined;
      if (!op) continue;
      const concrete = path.replace(/\{[^}]+\}/g, 'placeholder');
      if (op.security && op.security.length > 0) privatePaths.add(concrete);
      else publicPaths.add(concrete);
    }
  }
  return { publicPaths: [...publicPaths].sort(), privatePaths: [...privatePaths].sort() };
}

const { publicPaths, privatePaths } = operations();

/** Runs one request through the middleware and reports if it refreshed. */
async function refreshedFor(path: string): Promise<boolean> {
  const opts = {
    authApiBaseUrl: 'https://auth.example.com',
    getAccessToken: (): string | undefined => undefined,
    setAccessToken: vi.fn(),
    clearSession: vi.fn(),
  };
  const refresher = createTokenRefresher(opts);
  const mw = createAuthRequestMiddleware({
    getAccessToken: () => undefined,
    refresher,
  });
  await mw.onRequest({ request: new Request(`https://api.example.com${path}`) });
  return vi.mocked(globalThis.fetch).mock.calls.length > 0;
}

describe('public operations match the spec', () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    globalThis.fetch = vi.fn(
      async () => new Response(JSON.stringify({ accessToken: 'tok' }), { status: 200 }),
    );
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('found operations of both kinds in the spec', () => {
    // Guards the two assertions below from passing on an empty list if
    // the spec ever stops declaring security at all.
    expect(publicPaths.length).toBeGreaterThan(5);
    expect(privatePaths.length).toBeGreaterThan(50);
  });

  it.each(publicPaths)('%s needs no refresh', async (path) => {
    expect(await refreshedFor(path)).toBe(false);
  });

  it.each(privatePaths)('%s still refreshes', async (path) => {
    expect(await refreshedFor(path)).toBe(true);
  });
});
