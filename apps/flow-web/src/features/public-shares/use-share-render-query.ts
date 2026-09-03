/**
 * useShareRenderQuery — fetches the public calendar share payload for a
 * URL token. Shared by the branded `/share/cal/$token` page and the
 * chromeless `/embed/cal/$token` iframe route so both surfaces hit the
 * same SDK op (`GET /share/cal/{token}`) with identical caching.
 *
 * `throwOnError` is explicitly disabled so each route can render its own
 * branded invalid/expired fallback instead of bubbling up to the root
 * ErrorBoundary (which would show the generic fatal screen to anonymous
 * visitors). Terminal share errors (expired / invalid token) skip the
 * retry loop.
 */

import type { components } from '@nodate-flow/sdk';
import { useQuery } from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import { ApiError } from '../../lib/api-error';

type SharePageDTO = components['schemas']['PublicShareRenderPage'];
type ShareEventDTO = components['schemas']['PublicShareRenderEvent'];

/**
 * Normalised view-model returned by `useShareRenderQuery`. Differs from
 * the raw SDK body only in that `events` is collapsed from `T[] | null`
 * to `T[]`; both surfaces render the empty list directly.
 */
export interface NormalisedShareRender {
  page: SharePageDTO;
  events: ShareEventDTO[];
}

/** True for share errors that should not be retried. */
export function isTerminalShareError(code: string | undefined): boolean {
  return code === 'SHARE.SHARE.EXPIRED' || code === 'SHARE.SHARE.TOKEN_INVALID';
}

/** TanStack Query hook for the public share render payload. */
export function useShareRenderQuery(token: string) {
  return useQuery({
    queryKey: ['share', 'cal', token],
    queryFn: async (): Promise<NormalisedShareRender> => {
      const data = await apiRequest(
        (client) => client.GET('/share/cal/{token}', { params: { path: { token } } }),
        'Failed to load shared calendar',
      );
      return {
        page: data.page,
        events: data.events ?? [],
      };
    },
    retry: (count, err) => {
      if (err instanceof ApiError && isTerminalShareError(err.code)) {
        return false;
      }
      return count < 2;
    },
    throwOnError: false,
  });
}
