/**
 * Sharing feature — mutations for publishing/unpublishing lenses and a
 * query for fetching public lens data without authentication.
 *
 * All calls go through the typed `@nodate-flow/sdk` so request and
 * response shapes stay aligned with the OpenAPI contract. The public
 * lens query goes through the SDK too — its request still works
 * unauthenticated because openapi-fetch only attaches a bearer when the
 * token provider returns one, and the SDK's per-request auth middleware
 * is a no-op against the `/public/*` namespace.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';

import { ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

/**
 * Public lens data returned by GET /public/lenses/{token}.
 *
 * Sourced directly from the SDK so the wire shape and this view stay
 * in lockstep with the backend `PublicLens` DTO (description + the
 * resolved `tasks` array are part of the contract).
 */
export type PublicLensData = components['schemas']['PublicLens'];

/** Minimal task representation in a public lens. */
export type PublicTask = components['schemas']['PublicLensTask'];

/** Response from POST /workspaces/{wsId}/lenses/{lensId}/publish. */
export type PublishResult = components['schemas']['PublishLensBody'];

/** Query key factory for the sharing feature. */
export const sharingKeys = {
  all: ['sharing'] as const,
  publicLens: (token: string) => [...sharingKeys.all, 'public-lens', token] as const,
};

export { ApiError as SharingApiError };

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
      const { data, error } = await sdk.POST('/workspaces/{wsId}/lenses/{lensId}/publish', {
        params: { path: { wsId, lensId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to publish lens');
      return data;
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
      const { error } = await sdk.POST('/workspaces/{wsId}/lenses/{lensId}/unpublish', {
        params: { path: { wsId, lensId } },
      });
      if (error) throw toApiError(error, 'Failed to unpublish lens');
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
      const { data, error } = await sdk.GET('/public/lenses/{token}', {
        params: { path: { token } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load public view');
      return data as PublicLensData;
    },
  });
}
