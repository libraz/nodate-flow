/**
 * Sharing feature — mutations for publishing/unpublishing lenses and a
 * query for fetching public lens data without authentication.
 *
 * Publish/unpublish use raw fetch because these endpoints may not yet be
 * in the generated SDK. The public lens query also uses raw fetch since
 * it requires no auth token.
 */

import {
  type UseMutationResult,
  type UseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';

import { apiBaseUrl } from '../../lib/sdk';
import { authStore } from '../auth/auth-store';

/** Public lens data returned by GET /public/lenses/{token}. */
export interface PublicLensData {
  name: string;
  description: string | null;
  tasks: PublicTask[];
}

/** Minimal task representation in a public lens. */
export interface PublicTask {
  id: string;
  title: string;
  status: string;
  priority: number;
  dueOn: string | null;
  assigneeDisplayName: string | null;
}

/** Response from POST /workspaces/{wsId}/lenses/{lensId}/publish. */
export interface PublishResult {
  publicToken: string;
}

/** Query key factory for the sharing feature. */
export const sharingKeys = {
  all: ['sharing'] as const,
  publicLens: (token: string) => [...sharingKeys.all, 'public-lens', token] as const,
};

import { ApiError, toApiError } from '../../lib/api-error';

export { ApiError as SharingApiError };

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

/** Arguments for publish/unpublish mutations. */
export interface LensMutationArgs {
  lensId: string;
}

/**
 * usePublishLens — mutation that publishes a lens and returns the public
 * token used to construct the shareable URL.
 */
export function usePublishLens(
  wsId: string,
): UseMutationResult<PublishResult, ApiError, LensMutationArgs> {
  const qc = useQueryClient();
  return useMutation<PublishResult, ApiError, LensMutationArgs>({
    mutationFn: async ({ lensId }): Promise<PublishResult> => {
      return fetchJson<PublishResult>(`${apiBaseUrl}/workspaces/${wsId}/lenses/${lensId}/publish`, {
        method: 'POST',
      });
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: sharingKeys.all });
    },
  });
}

/**
 * useUnpublishLens — mutation that revokes public access for a lens.
 */
export function useUnpublishLens(
  wsId: string,
): UseMutationResult<void, ApiError, LensMutationArgs> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, LensMutationArgs>({
    mutationFn: async ({ lensId }): Promise<void> => {
      await fetchJson<{ ok: boolean }>(
        `${apiBaseUrl}/workspaces/${wsId}/lenses/${lensId}/unpublish`,
        { method: 'POST' },
      );
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: sharingKeys.all });
    },
  });
}

/**
 * usePublicLensQuery — fetches public lens data without authentication.
 *
 * This is a non-suspense query because the public page does not live
 * inside the authenticated layout with its ErrorBoundary hierarchy.
 */
export function usePublicLensQuery(token: string): UseQueryResult<PublicLensData, ApiError> {
  return useQuery<PublicLensData, ApiError>({
    queryKey: sharingKeys.publicLens(token),
    queryFn: async (): Promise<PublicLensData> => {
      const res = await fetch(`${apiBaseUrl}/public/lenses/${token}`);
      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as unknown;
        throw toApiError(body, 'Failed to load public view');
      }
      return (await res.json()) as PublicLensData;
    },
  });
}
