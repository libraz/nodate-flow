/**
 * Pages feature — query key factory, types, and hooks for
 * page CRUD, child pages, search, and AI generation.
 *
 * Calls go through the typed `@nodate-flow/sdk` so request and response
 * shapes stay aligned with the OpenAPI contract.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import { ApiError } from '../../lib/api-error';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/**
 * Unified page view used by detail screens and tree/list views.
 *
 * The list endpoints return `PageSummaryDTO` (no `body`, no
 * `parentPageTitle`); the get endpoint returns the full `PageDTO`.
 * We widen the local type to `body?` / `parentPageTitle?` so the same
 * `PageItem` shape covers both surfaces.
 */
export interface PageItem
  extends Omit<components['schemas']['PageDTO'], '$schema' | 'body' | 'parentPageTitle'> {
  body?: string;
  parentPageTitle?: string;
}

/** Body for POST /workspaces/{wsId}/pages. */
export type CreatePageInput = Omit<components['schemas']['CreatePageBody'], '$schema'>;

/** Body for PATCH /workspaces/{wsId}/pages/{pageId}. */
export type UpdatePageInput = Omit<components['schemas']['UpdatePageBody'], '$schema'>;

// ---------------------------------------------------------------------------
// Query key factory
// ---------------------------------------------------------------------------

/** Query key factory for the pages feature. */
export const pageKeys = {
  all: ['pages'] as const,
  list: (wsId: string) => [...pageKeys.all, 'list', wsId] as const,
  children: (pageId: string) => [...pageKeys.all, 'children', pageId] as const,
  detail: (pageId: string) => [...pageKeys.all, 'detail', pageId] as const,
  search: (wsId: string, q: string) => [...pageKeys.all, 'search', wsId, q] as const,
};

// ---------------------------------------------------------------------------
// Error helper
// ---------------------------------------------------------------------------

export { ApiError as PageApiError };

// ---------------------------------------------------------------------------
// Query hooks
// ---------------------------------------------------------------------------

/** GET /workspaces/{wsId}/pages — suspense query for root pages. */
export function usePagesQuery(wsId: string): UseSuspenseQueryResult<PageItem[]> {
  return useSuspenseQuery({
    queryKey: pageKeys.list(wsId),
    queryFn: async (): Promise<PageItem[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/pages', {
            params: { path: { wsId }, query: { limit: 200 } },
          }),
        'Failed to load pages',
      );
      return (data.pages ?? []) as PageItem[];
    },
  });
}

/** GET /workspaces/{wsId}/pages/{pageId}/children — suspense query for child pages. */
export function useChildPagesQuery(
  wsId: string,
  pageId: string,
): UseSuspenseQueryResult<PageItem[]> {
  return useSuspenseQuery({
    queryKey: pageKeys.children(pageId),
    queryFn: async (): Promise<PageItem[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/pages/{pageId}/children', {
            params: { path: { wsId, pageId }, query: { limit: 200 } },
          }),
        'Failed to load child pages',
      );
      return (data.pages ?? []) as PageItem[];
    },
  });
}

/** GET /workspaces/{wsId}/pages/{pageId} — suspense query for a single page. */
export function usePageQuery(wsId: string, pageId: string): UseSuspenseQueryResult<PageItem> {
  return useSuspenseQuery({
    queryKey: pageKeys.detail(pageId),
    queryFn: async (): Promise<PageItem> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/pages/{pageId}', {
            params: { path: { wsId, pageId } },
          }),
        'Failed to load page',
      );
      return data as PageItem;
    },
  });
}

/** GET /workspaces/{wsId}/pages/search?q=... — non-suspense for debounced search. */
export function useSearchPages(wsId: string, query: string): UseQueryResult<PageItem[]> {
  return useQuery({
    queryKey: pageKeys.search(wsId, query),
    enabled: query.length >= 2,
    queryFn: async (): Promise<PageItem[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/pages/search', {
            params: { path: { wsId }, query: { q: query, limit: 50 } },
          }),
        'Failed to search pages',
      );
      return (data.pages ?? []) as PageItem[];
    },
  });
}

// ---------------------------------------------------------------------------
// Mutation hooks
// ---------------------------------------------------------------------------

export interface CreatePageArgs {
  input: CreatePageInput;
}

/** POST /workspaces/{wsId}/pages — create a new page. */
export function useCreatePage(wsId: string): UseMutationResult<PageItem, ApiError, CreatePageArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ input }: CreatePageArgs): Promise<PageItem> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/pages', {
            params: { path: { wsId } },
            body: input,
          }),
        'Failed to create page',
      );
      return data as PageItem;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: pageKeys.list(wsId) });
      if (vars.input.parentPageId) {
        void qc.invalidateQueries({ queryKey: pageKeys.children(vars.input.parentPageId) });
      }
    },
  });
}

export interface UpdatePageArgs {
  pageId: string;
  patch: UpdatePageInput;
}

/** PATCH /workspaces/{wsId}/pages/{pageId} — update a page. */
export function useUpdatePage(wsId: string): UseMutationResult<PageItem, ApiError, UpdatePageArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ pageId, patch }: UpdatePageArgs): Promise<PageItem> => {
      const data = await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/pages/{pageId}', {
            params: { path: { wsId, pageId } },
            body: patch,
          }),
        'Failed to update page',
      );
      return data as PageItem;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: pageKeys.list(wsId) });
      void qc.invalidateQueries({ queryKey: pageKeys.detail(vars.pageId) });
    },
  });
}

/** DELETE /workspaces/{wsId}/pages/{pageId} — soft delete. */
export function useDeletePage(wsId: string): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (pageId: string): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/pages/{pageId}', {
            params: { path: { wsId, pageId } },
          }),
        'Failed to delete page',
      );
    },
    onSuccess: (_data, pageId) => {
      void qc.invalidateQueries({ queryKey: pageKeys.list(wsId) });
      qc.removeQueries({ queryKey: pageKeys.children(pageId) });
      qc.removeQueries({ queryKey: pageKeys.detail(pageId) });
    },
  });
}

export interface GeneratePageArgs {
  title: string;
  prompt: string;
  projectId?: string | undefined;
}

/** POST /workspaces/{wsId}/pages/generate — AI page generation. */
export function useGeneratePage(
  wsId: string,
): UseMutationResult<PageItem, ApiError, GeneratePageArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (args: GeneratePageArgs): Promise<PageItem> => {
      const body: components['schemas']['GeneratePageBody'] = {
        title: args.title,
        prompt: args.prompt,
        ...(args.projectId !== undefined ? { projectId: args.projectId } : {}),
      };
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/pages/generate', {
            params: { path: { wsId } },
            body,
          }),
        'Failed to generate page',
      );
      return data as PageItem;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: pageKeys.list(wsId) });
    },
  });
}
