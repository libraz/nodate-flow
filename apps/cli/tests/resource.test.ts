import { describe, expect, it, vi } from 'vitest';

import {
  buildListQuery,
  executeProjectList,
  executeProjectListPaginated,
  executeWorkspaceList,
  executeWorkspaceListPaginated,
  type SdkClientLike,
} from '../src/resource-builders.js';

function createSdkMock(response: { data?: unknown; error?: unknown }): SdkClientLike {
  return {
    GET: vi.fn().mockResolvedValue(response),
  };
}

/** Builds a page of `count` rows keyed by `field`, starting at `offset`. */
function page(field: 'workspaces' | 'projects', offset: number, count: number, total: number) {
  return {
    data: {
      [field]: Array.from({ length: count }, (_, i) => ({ id: `${field}-${offset + i}` })),
      total,
      nextCursor: null,
    },
  };
}

describe('buildListQuery', () => {
  it("defaults to the endpoint's own page size", () => {
    expect(buildListQuery(undefined, undefined)).toEqual({ limit: 50 });
  });

  it('forwards explicit limit and offset', () => {
    expect(buildListQuery(25, 50)).toEqual({ limit: 25, offset: 50 });
  });
});

describe('executeWorkspaceList', () => {
  it('issues GET /workspaces with query parameters', async () => {
    const sdk = createSdkMock({ data: { workspaces: [], total: 0, nextCursor: null } });
    await executeWorkspaceList(sdk, { limit: 25, offset: 50 });

    expect(sdk.GET).toHaveBeenCalledWith('/workspaces', {
      params: { query: { limit: 25, offset: 50 } },
    });
  });
});

describe('executeProjectList', () => {
  it('issues GET /workspaces/{wsId}/projects with path and query parameters', async () => {
    const sdk = createSdkMock({ data: { projects: [], total: 0, nextCursor: null } });
    await executeProjectList(sdk, 'ws-1', { limit: 100 });

    expect(sdk.GET).toHaveBeenCalledWith('/workspaces/{wsId}/projects', {
      params: { path: { wsId: 'ws-1' }, query: { limit: 100 } },
    });
  });
});

/*
 * Both list endpoints declare `maximum:"200"` on `limit` and answer a
 * larger value with a 422 instead of clamping it, so the paginated
 * executors have to split the request themselves.
 */

describe('executeWorkspaceListPaginated', () => {
  it('splits a limit above the page maximum across several requests', async () => {
    const sdk: SdkClientLike = {
      GET: vi
        .fn()
        .mockResolvedValueOnce(page('workspaces', 0, 200, 500))
        .mockResolvedValueOnce(page('workspaces', 200, 200, 500))
        .mockResolvedValueOnce(page('workspaces', 400, 100, 500)),
    };

    const result = await executeWorkspaceListPaginated(sdk, { limit: 500 });

    expect(result.data?.items).toHaveLength(500);
    expect(result.data?.total).toBe(500);
    expect(sdk.GET).toHaveBeenNthCalledWith(1, '/workspaces', {
      params: { query: { limit: 200, offset: 0 } },
    });
    expect(sdk.GET).toHaveBeenNthCalledWith(2, '/workspaces', {
      params: { query: { limit: 200, offset: 200 } },
    });
    expect(sdk.GET).toHaveBeenNthCalledWith(3, '/workspaces', {
      params: { query: { limit: 200, offset: 400 } },
    });
  });

  it('stops at the row count the server reports', async () => {
    const sdk = createSdkMock(page('workspaces', 0, 3, 3));

    const result = await executeWorkspaceListPaginated(sdk, { limit: 500 });

    expect(result.data?.items).toHaveLength(3);
    expect(sdk.GET).toHaveBeenCalledTimes(1);
  });

  it('honours a starting offset', async () => {
    const sdk = createSdkMock(page('workspaces', 10, 2, 12));

    await executeWorkspaceListPaginated(sdk, { limit: 2, offset: 10 });

    expect(sdk.GET).toHaveBeenCalledWith('/workspaces', {
      params: { query: { limit: 2, offset: 10 } },
    });
  });

  it('forwards page errors without issuing later requests', async () => {
    const sdk = createSdkMock({ error: { detail: 'boom' } });

    const result = await executeWorkspaceListPaginated(sdk, { limit: 500 });

    expect(result.error).toEqual({ detail: 'boom' });
    expect(sdk.GET).toHaveBeenCalledTimes(1);
  });
});

describe('executeProjectListPaginated', () => {
  it('splits a limit above the page maximum across several requests', async () => {
    const sdk: SdkClientLike = {
      GET: vi
        .fn()
        .mockResolvedValueOnce(page('projects', 0, 200, 300))
        .mockResolvedValueOnce(page('projects', 200, 100, 300)),
    };

    const result = await executeProjectListPaginated(sdk, 'ws-1', { limit: 500 });

    expect(result.data?.items).toHaveLength(300);
    expect(result.data?.total).toBe(300);
    expect(sdk.GET).toHaveBeenNthCalledWith(1, '/workspaces/{wsId}/projects', {
      params: { path: { wsId: 'ws-1' }, query: { limit: 200, offset: 0 } },
    });
    expect(sdk.GET).toHaveBeenNthCalledWith(2, '/workspaces/{wsId}/projects', {
      params: { path: { wsId: 'ws-1' }, query: { limit: 200, offset: 200 } },
    });
  });

  it('stops when a page comes back empty', async () => {
    const sdk: SdkClientLike = {
      GET: vi
        .fn()
        .mockResolvedValueOnce(page('projects', 0, 5, 999))
        .mockResolvedValueOnce(page('projects', 5, 0, 999)),
    };

    const result = await executeProjectListPaginated(sdk, 'ws-1', { limit: 500 });

    expect(result.data?.items).toHaveLength(5);
    expect(sdk.GET).toHaveBeenCalledTimes(2);
  });
});
