/**
 * MSW server for component / hook tests.
 *
 * Interception happens at the fetch boundary, which is the point of it:
 * `vi.mock('../../lib/sdk')` replaces the generated client with a
 * hand-written stand-in, so a test written that way still passes when
 * the real client stops sending what the test thinks it sends — a
 * path parameter that never gets substituted, a renamed field, a
 * different error envelope. Those are exactly the breakages the
 * OpenAPI → SDK codegen chain is supposed to catch, and mocking the
 * SDK is precisely the layer that hides them. Going through MSW means
 * the assertions see the URL, the headers, and the body that would
 * have gone to the API.
 *
 * `onUnhandledRequest: 'error'` is deliberate: a request no handler
 * covers is a hole in the test, not something to answer with a default.
 *
 * Handlers are registered per test with `server.use(...)`; this module
 * only owns the lifecycle.
 */

import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll } from 'vitest';

export const server = setupServer();

/**
 * Base URL the app's SDK client points at under test. Mirrors the
 * default in `src/lib/sdk.ts` so handlers can be written against the
 * same origin the client will actually call.
 */
export const AUTH_API_URL = 'http://localhost:8082';

/**
 * Starts MSW for the current test file. Call once at module scope.
 */
export function useMockApi(): void {
  beforeAll(() => {
    server.listen({ onUnhandledRequest: 'error' });
  });
  afterEach(() => {
    server.resetHandlers();
  });
  afterAll(() => {
    server.close();
  });
}
