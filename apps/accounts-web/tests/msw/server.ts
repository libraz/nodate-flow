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
 * `onUnhandledFrame: 'error'` is deliberate: a request no handler
 * covers is a hole in the test, not something to answer with a default.
 *
 * Handlers are registered per test with `server.use(...)`; this module
 * only owns the lifecycle.
 */

import type { Window as HappyDOMWindow } from 'happy-dom';
import { HttpResponse, http } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll } from 'vitest';

function localApiPreflight(request: Request): HttpResponse<null> {
  const headers = new Headers({
    'access-control-allow-origin': window.location.origin,
    'access-control-allow-credentials': 'true',
    'access-control-allow-methods': 'GET, POST, PUT, PATCH, DELETE, OPTIONS',
    'access-control-allow-headers':
      request.headers.get('access-control-request-headers') ??
      'accept, authorization, content-type, x-requested-with',
  });

  return new HttpResponse(null, { status: 204, headers });
}

// happy-dom 21 models fetch's browser CORS preflight. Consume only OPTIONS
// requests to the local auth API mock; actual API requests still require a
// test handler under the strict unhandled-request strategy below.
export const server = setupServer(
  http.options(/^http:\/\/localhost:8082\//, ({ request }) => localApiPreflight(request)),
);

/**
 * Base URL the app's SDK client points at under test. Mirrors the
 * default in `src/lib/sdk.ts` so handlers can be written against the
 * same origin the client will actually call.
 */
export const AUTH_API_URL = 'http://localhost:8082';

function enableLocalMockApiCors(): () => void {
  const fetchSettings = (window as unknown as HappyDOMWindow).happyDOM.settings.fetch;
  const previousInterceptor = fetchSettings.interceptor;

  fetchSettings.interceptor = {
    ...previousInterceptor,
    afterAsyncResponse: async (context) => {
      const response =
        (await previousInterceptor?.afterAsyncResponse?.(context)) ?? context.response;
      const requestUrl = new URL(context.request.url);
      const origin = context.request.headers.get('origin');

      if (
        origin &&
        new URL(origin).hostname === 'localhost' &&
        requestUrl.origin === AUTH_API_URL
      ) {
        response.headers.set('access-control-allow-origin', origin);
        response.headers.set('access-control-allow-credentials', 'true');
      }

      return response;
    },
  };

  return () => {
    fetchSettings.interceptor = previousInterceptor;
  };
}

/**
 * Starts MSW for the current test file. Call once at module scope.
 */
export function useMockApi(): void {
  let restoreFetchInterceptor = (): void => {};

  beforeAll(() => {
    restoreFetchInterceptor = enableLocalMockApiCors();
    server.listen({ onUnhandledFrame: 'error' });
  });
  afterEach(() => {
    server.resetHandlers();
  });
  afterAll(() => {
    server.close();
    restoreFetchInterceptor();
  });
}
