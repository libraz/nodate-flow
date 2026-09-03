/**
 * NL command feature — mutation hook for resolving natural language
 * commands via the AI resolve-command endpoint.
 *
 * Goes through the typed `@nodate-flow/sdk` so request and response
 * shapes stay in lockstep with the OpenAPI contract.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseMutationResult, useMutation } from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import { ApiError } from '../../lib/api-error';

/** Result returned by POST /workspaces/{wsId}/ai/resolve-command. */
export type ResolveCommandResult = components['schemas']['ResolveCommandOutputBody'];

/** Resolves a natural language prompt into a tool invocation. */
export function useResolveCommand(
  wsId: string | null,
): UseMutationResult<ResolveCommandResult, ApiError, string> {
  return useMutation<ResolveCommandResult, ApiError, string>({
    mutationFn: async (prompt: string): Promise<ResolveCommandResult> => {
      if (!wsId) throw new ApiError(undefined, 'No workspace selected');
      return apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/ai/resolve-command', {
            params: { path: { wsId } },
            body: { prompt },
          }),
        'Failed to resolve command',
      );
    },
  });
}
