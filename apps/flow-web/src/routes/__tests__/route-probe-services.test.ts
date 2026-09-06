/**
 * Every route loader that probes for existence must ask the service that
 * owns the resource.
 *
 * A probe reports a status and never throws, so a probe sent to the
 * wrong service is indistinguishable from a legitimate answer: the
 * service that does not serve the path replies 404, the loader reads
 * "gone", and the route renders its not-found screen for a resource that
 * is perfectly present. Nothing about the loader's return value gives
 * this away — which is why the assertion below is the origin the request
 * reached, read off the wire through MSW, and not whether the loader
 * resolved.
 *
 * The call sites are covered as one table rather than one file per
 * route. The rule being pinned is a property of the whole set ("the
 * probe goes to the owner"), and a table makes adding a probing loader a
 * one-entry change; a per-route test leaves the rule stated nowhere, so
 * the next loader is written without it and covered only if someone
 * remembers to write the file.
 */

import { isNotFound } from '@tanstack/react-router';
import { HttpResponse, http } from 'msw';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { API_URL, AUTH_API_URL, server, useMockApi } from '../../../tests/msw/server';
import { authStore } from '../../features/auth/auth-store';
import { ApiError } from '../../lib/api-error';
import { Route as TaskRoute } from '../_authenticated.tasks.$taskId';
import { Route as WorkspaceRoute } from '../_authenticated.workspaces.$id';

useMockApi();

/** A route loader as the router calls it, narrowed to the part we drive. */
type Loader = (args: { params: Record<string, string> }) => Promise<unknown>;

interface ProbeCallSite {
  /** The route as it reads in the URL bar. */
  route: string;
  /** Origin of the service that owns the resource. */
  owner: string;
  /** Origin of the service that serves no such path. */
  stranger: string;
  /** Path the probe has to request for a given id. */
  pathFor: (id: string) => string;
  /** Runs the route's loader for a given id. */
  load: (id: string) => Promise<unknown>;
  /** True when the thrown value is this route's not-found signal. */
  signalsNotFound: (err: unknown) => boolean;
}

function runLoader(route: { options: { loader?: unknown } }, params: Record<string, string>) {
  return (route.options.loader as Loader)({ params });
}

const CALL_SITES: ProbeCallSite[] = [
  {
    route: '/workspaces/$id',
    owner: AUTH_API_URL,
    stranger: API_URL,
    pathFor: (id) => `/workspaces/${id}`,
    load: (id) => runLoader(WorkspaceRoute, { id }),
    signalsNotFound: (err) => err instanceof ApiError && err.code === 'WS.WORKSPACE.NOT_FOUND',
  },
  {
    route: '/tasks/$taskId',
    owner: API_URL,
    stranger: AUTH_API_URL,
    pathFor: (id) => `/tasks/${id}`,
    load: (id) => runLoader(TaskRoute, { taskId: id }),
    signalsNotFound: (err) => isNotFound(err),
  },
];

/** The id both services are asked about when the resource is there. */
const PRESENT = 'a1b2c3d4-0000-7000-8000-00000000beef';
/** An id no service knows. */
const ABSENT = 'ffffffff-0000-7000-8000-0000000000ff';

/** Every request MSW answered during the current test, in order. */
let seen: URL[] = [];

beforeEach(() => {
  seen = [];
  // An opaque (non-JWT) token keeps the proactive refresh middleware
  // from firing, so the only request a test records is the probe.
  authStore.getState().setAccessToken('probe-test-token');
});

afterEach(() => {
  authStore.getState().clearSession();
});

/**
 * Answers the probe path on both origins and records where each call
 * landed. The owner knows {@link PRESENT}; the stranger answers 404 for
 * every id, the way a router replies to a path it does not serve — the
 * answer that lets a misdirected probe pass for a real "not found".
 */
function serveOnBothServices(site: ProbeCallSite): void {
  const record = (origin: string, respond: (id: string) => Response) =>
    http.get(`${origin}${site.pathFor(':id')}`, ({ request, params }) => {
      seen.push(new URL(request.url));
      return respond(String(params.id));
    });

  server.use(
    record(site.owner, (id) =>
      id === PRESENT
        ? HttpResponse.json({ id })
        : new HttpResponse(null, { status: 404, statusText: 'Not Found' }),
    ),
    record(site.stranger, () => new HttpResponse(null, { status: 404, statusText: 'Not Found' })),
  );
}

describe.each(CALL_SITES)('$route loader', (site) => {
  beforeEach(() => {
    serveOnBothServices(site);
  });

  it('probes the service that owns the resource, at the resource path', async () => {
    await site.load(PRESENT);

    expect(seen).toHaveLength(1);
    expect(seen[0]?.origin).toBe(site.owner);
    expect(seen[0]?.pathname).toBe(site.pathFor(PRESENT));
    expect(seen.map((url) => url.origin)).not.toContain(site.stranger);
  });

  it('lets a resource that exists load without the not-found state', async () => {
    await expect(site.load(PRESENT)).resolves.toBeNull();
  });

  it('still reports not found for an id the owning service does not know', async () => {
    // The fix redirects the probe; it must not have turned the existence
    // check into a no-op, so a genuine miss still reaches the same
    // signal the route's not-found screen is keyed off.
    await expect(site.load(ABSENT)).rejects.toSatisfy(site.signalsNotFound);
  });
});
