import type { components, NodateFlowClient } from '@nodate-flow/sdk';

export type SdkClientLike = Pick<NodateFlowClient, 'GET'>;

export interface ListQuery {
  limit: number;
  offset?: number;
}

export type WorkspaceListPage = components['schemas']['WorkspacesListOutputBody'];
export type ProjectListPage = components['schemas']['ListProjectsBody'];

export function buildListQuery(limit: number | undefined, offset: number | undefined): ListQuery {
  const query: ListQuery = { limit: limit ?? 100 };
  if (offset !== undefined) query.offset = offset;
  return query;
}

export async function executeWorkspaceList(
  client: SdkClientLike,
  query: ListQuery,
): Promise<{ data?: WorkspaceListPage; error?: unknown }> {
  return client.GET('/workspaces', { params: { query } });
}

export async function executeProjectList(
  client: SdkClientLike,
  workspaceId: string,
  query: ListQuery,
): Promise<{ data?: ProjectListPage; error?: unknown }> {
  return client.GET('/workspaces/{wsId}/projects', {
    params: { path: { wsId: workspaceId }, query },
  });
}
