/**
 * Pages feature — query key factory, types, and hooks for
 * page CRUD, child pages, search, and AI generation.
 *
 * Types are defined inline because the SDK may not yet include these
 * endpoints. API calls use raw fetch via the shared base URL and auth
 * store token (same pattern as timeboxes).
 */

import {
  type UseMutationResult,
  type UseQueryResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { apiBaseUrl } from '../../lib/sdk';
import { authStore } from '../auth/auth-store';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** Page item returned by the list / detail API. Timestamps are unix seconds. */
export interface PageItem {
  id: string;
  title: string;
  body?: string;
  projectId?: string;
  projectName?: string;
  creatorId: string;
  creatorDisplayName: string;
  parentPageId?: string;
  parentPageTitle?: string;
  isAiGenerated: boolean;
  updatedAt: number;
  createdAt: number;
  total: number;
}

/** Body for POST /workspaces/{wsId}/pages. */
export interface CreatePageInput {
  title: string;
  body?: string | undefined;
  parentPageId?: string | undefined;
  projectId?: string | undefined;
}

/** Body for PATCH /workspaces/{wsId}/pages/{pageId}. */
export interface UpdatePageInput {
  title?: string | undefined;
  body?: string | undefined;
  parentPageId?: string | undefined;
}

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

import { ApiError, toApiError } from '../../lib/api-error';

export { ApiError as PageApiError };

// ---------------------------------------------------------------------------
// Fetch helpers
// ---------------------------------------------------------------------------

function authHeaders(): HeadersInit {
  const token = authStore.getState().accessToken;
  // biome-ignore lint/style/useNamingConvention: HTTP header name
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function fetchJson<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    credentials: 'include',
    headers: {
      ...authHeaders(),
      ...init?.headers,
    },
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as unknown;
    throw toApiError(body, `Request failed with status ${String(res.status)}`);
  }
  return (await res.json()) as T;
}

async function fetchVoid(url: string, init?: RequestInit): Promise<void> {
  const res = await fetch(url, {
    ...init,
    credentials: 'include',
    headers: {
      ...authHeaders(),
      ...init?.headers,
    },
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as unknown;
    throw toApiError(body, `Request failed with status ${String(res.status)}`);
  }
}

// ---------------------------------------------------------------------------
// Query hooks
// ---------------------------------------------------------------------------

/** GET /workspaces/{wsId}/pages — suspense query for root pages. */
export function usePagesQuery(wsId: string): UseSuspenseQueryResult<PageItem[]> {
  return useSuspenseQuery({
    queryKey: pageKeys.list(wsId),
    queryFn: async (): Promise<PageItem[]> => {
      const data = await fetchJson<{ items?: PageItem[] }>(
        `${apiBaseUrl}/workspaces/${wsId}/pages?limit=200`,
      );
      return data.items ?? [];
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
      const data = await fetchJson<{ items?: PageItem[] }>(
        `${apiBaseUrl}/workspaces/${wsId}/pages/${pageId}/children?limit=200`,
      );
      return data.items ?? [];
    },
  });
}

/** GET /workspaces/{wsId}/pages/{pageId} — suspense query for a single page. */
export function usePageQuery(wsId: string, pageId: string): UseSuspenseQueryResult<PageItem> {
  return useSuspenseQuery({
    queryKey: pageKeys.detail(pageId),
    queryFn: async (): Promise<PageItem> => {
      return fetchJson<PageItem>(`${apiBaseUrl}/workspaces/${wsId}/pages/${pageId}`);
    },
  });
}

/** GET /workspaces/{wsId}/pages/search?q=... — non-suspense for debounced search. */
export function useSearchPages(wsId: string, query: string): UseQueryResult<PageItem[]> {
  return useQuery({
    queryKey: pageKeys.search(wsId, query),
    enabled: query.length >= 2,
    queryFn: async (): Promise<PageItem[]> => {
      const data = await fetchJson<{ items?: PageItem[] }>(
        `${apiBaseUrl}/workspaces/${wsId}/pages/search?q=${encodeURIComponent(query)}&limit=50`,
      );
      return data.items ?? [];
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
      return fetchJson<PageItem>(`${apiBaseUrl}/workspaces/${wsId}/pages`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      });
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
      return fetchJson<PageItem>(`${apiBaseUrl}/workspaces/${wsId}/pages/${pageId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(patch),
      });
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
      await fetchVoid(`${apiBaseUrl}/workspaces/${wsId}/pages/${pageId}`, {
        method: 'DELETE',
      });
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: pageKeys.list(wsId) });
    },
  });
}

export interface GeneratePageArgs {
  title: string;
  parentPageId?: string | undefined;
  projectId?: string | undefined;
}

/** POST /workspaces/{wsId}/pages/generate — AI page generation. */
export function useGeneratePage(
  wsId: string,
): UseMutationResult<PageItem, ApiError, GeneratePageArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (args: GeneratePageArgs): Promise<PageItem> => {
      return fetchJson<PageItem>(`${apiBaseUrl}/workspaces/${wsId}/pages/generate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(args),
      });
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: pageKeys.list(wsId) });
    },
  });
}
