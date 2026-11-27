/**
 * Workspace AI status — which AI capabilities work right now.
 *
 * Readable by every workspace role, guests included. Non-suspense so the
 * status card shows its own loading and failure states without taking the
 * settings page down.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseQueryResult, useQuery } from '@tanstack/react-query';

import { apiRequest } from '../../lib/api';
import type { ApiError } from '../../lib/api-error';

export type AiStatus = components['schemas']['WorkspaceAIStatusBody'];

/** Query key factory for the workspace AI status. */
export const aiStatusKeys = {
  detail: (workspaceId: string) => ['ai-status', workspaceId] as const,
};

/** GET /workspaces/{wsId}/ai/status. */
export function useAiStatusQuery(workspaceId: string): UseQueryResult<AiStatus, ApiError> {
  return useQuery<AiStatus, ApiError>({
    queryKey: aiStatusKeys.detail(workspaceId),
    throwOnError: false,
    queryFn: () =>
      apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/ai/status', {
            params: { path: { wsId: workspaceId } },
          }),
        'Failed to load AI status',
      ),
  });
}
