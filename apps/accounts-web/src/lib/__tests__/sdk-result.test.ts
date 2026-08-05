/**
 * The failure a body-less error response used to slip through.
 *
 * openapi-fetch only fills `error` when the response carries a body it
 * can parse into the operation's declared error schema. A 403 with
 * `Content-Length: 0`, chi's default 405, a gateway 502 — all arrive
 * with `error` and `data` both undefined, so a handler that branched on
 * `error` alone ran its success path against a request the server
 * refused.
 */

import { describe, expect, it } from 'vitest';

import { requestFailed } from '../sdk-result';

/** Build the result shape openapi-fetch returns for a given status. */
function resultFor(status: number, error?: unknown): { error?: unknown; response: Response } {
  return { error, response: new Response(null, { status }) };
}

describe('requestFailed', () => {
  it('reports a body-less error response as a failure', () => {
    // The case that mattered: no error body, so `error` is undefined.
    expect(requestFailed(resultFor(403))).toBe(true);
    expect(requestFailed(resultFor(405))).toBe(true);
    expect(requestFailed(resultFor(502))).toBe(true);
  });

  it('reports a typed error response as a failure', () => {
    expect(requestFailed(resultFor(400, { detail: 'bad request' }))).toBe(true);
  });

  it('accepts a success with no body', () => {
    // 204 is how a successful revoke answers, and it must not be
    // mistaken for the body-less failures above.
    expect(requestFailed(resultFor(204))).toBe(false);
    expect(requestFailed(resultFor(200))).toBe(false);
  });

  it('treats a null error the way openapi-fetch treats an absent one', () => {
    expect(requestFailed(resultFor(200, null))).toBe(false);
  });

  it('reports a result with no response at all as a failure', () => {
    // Only a stub produces this. On a security action, an unknown
    // outcome has to read as "it did not happen".
    expect(requestFailed({})).toBe(true);
  });
});
