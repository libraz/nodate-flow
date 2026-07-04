import { describe, expect, it, vi } from 'vitest';

import {
  buildListQuery,
  executeProjectList,
  executeWorkspaceList,
  type SdkClientLike,
} from '../src/resource-builders.js';

function createSdkMock(response: { data?: unknown; error?: unknown }): SdkClientLike {
  return {
    // biome-ignore lint/style/useNamingConvention: SDK method name
    GET: vi.fn().mockResolvedValue(response),
  };
}

describe('buildListQuery', () => {
  it('defaults to a discovery-friendly limit', () => {
    expect(buildListQuery(undefined, undefined)).toEqual({ limit: 100 });
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
