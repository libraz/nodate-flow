/**
 * Pure builders and executors for `tnk workspace` / `tnk project`.
 *
 * Both list endpoints page by offset only — neither accepts a `cursor`
 * query parameter — so the paginated executors here walk offsets until
 * the caller's requested row count is satisfied.
 */

import type { components, NodateFlowClient } from '@nodate-flow/sdk';

import { clampPageLimit, DEFAULT_LIST_LIMIT, MAX_PAGES, requestedCount } from './util/paging.js';

export type SdkClientLike = Pick<NodateFlowClient, 'GET'>;

export interface ListQuery {
  limit: number;
  offset?: number;
}

export type WorkspaceListPage = components['schemas']['WorkspacesListOutputBody'];
export type ProjectListPage = components['schemas']['ListProjectsBody'];

/** Rows accumulated across every page, plus the server's row count. */
export interface ListResult {
  items: unknown[];
  total: number;
}

export function buildListQuery(limit: number | undefined, offset: number | undefined): ListQuery {
  const query: ListQuery = { limit: limit ?? DEFAULT_LIST_LIMIT };
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

/** One page as seen by {@link collectPages}. */
interface PageRows {
  rows: unknown[];
  total?: number | undefined;
  error?: unknown;
}

/**
 * Request pages of at most `MAX_PAGE_LIMIT` rows until `query.limit` rows
 * have been collected, the server runs out of rows, or a page fails.
 */
async function collectPages(
  fetchPage: (limit: number, offset: number) => Promise<PageRows>,
  query: ListQuery,
): Promise<{ data?: ListResult; error?: unknown }> {
  const wanted = requestedCount(query.limit);
  if (wanted === 0) return { data: { items: [], total: 0 } };

  const perPage = clampPageLimit(wanted);
  const items: unknown[] = [];
  let total: number | undefined;
  let offset = query.offset ?? 0;

  for (let page = 0; page < MAX_PAGES; page += 1) {
    const result = await fetchPage(perPage, offset);
    if (result.error) return { error: result.error };
    if (typeof result.total === 'number') total = result.total;

    items.push(...result.rows);
    if (items.length >= wanted) break;
    if (result.rows.length === 0) break;
    if (total !== undefined && items.length >= total) break;

    offset += result.rows.length;
  }

  return { data: { items: items.slice(0, wanted), total: total ?? items.length } };
}

/** Fetch as many pages of `GET /workspaces` as the caller asked for. */
export async function executeWorkspaceListPaginated(
  client: SdkClientLike,
  query: ListQuery,
): Promise<{ data?: ListResult; error?: unknown }> {
  return collectPages(async (limit, offset) => {
    const { data, error } = await executeWorkspaceList(client, { limit, offset });
    if (error) return { rows: [], error };
    return { rows: data?.workspaces ?? [], total: data?.total };
  }, query);
}

/** Fetch as many pages of `GET /workspaces/{wsId}/projects` as the caller asked for. */
export async function executeProjectListPaginated(
  client: SdkClientLike,
  workspaceId: string,
  query: ListQuery,
): Promise<{ data?: ListResult; error?: unknown }> {
  return collectPages(async (limit, offset) => {
    const { data, error } = await executeProjectList(client, workspaceId, { limit, offset });
    if (error) return { rows: [], error };
    return { rows: data?.projects ?? [], total: data?.total };
  }, query);
}
