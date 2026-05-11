/**
 * NL command feature — mutation hook for resolving natural language
 * commands via the AI resolve-command endpoint.
 *
 * Goes through the typed `@nodate-flow/sdk` so request and response
 * shapes stay in lockstep with the OpenAPI contract.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseMutationResult, useMutation } from '@tanstack/react-query';

import { ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

/** Result returned by POST /workspaces/{wsId}/ai/resolve-command. */
export type ResolveCommandResult = components['schemas']['ResolveCommandOutputBody'];

/** Resolves a natural language prompt into a tool invocation. */
export function useResolveCommand(
  wsId: string | null,
): UseMutationResult<ResolveCommandResult, ApiError, string> {
  return useMutation<ResolveCommandResult, ApiError, string>({
    mutationFn: async (prompt: string): Promise<ResolveCommandResult> => {
      if (!wsId) throw new ApiError(undefined, 'No workspace selected');
      const { data, error } = await sdk.POST('/workspaces/{wsId}/ai/resolve-command', {
        params: { path: { wsId } },
        body: { prompt },
      });
      if (error || !data) {
        throw toApiError(error, 'Failed to resolve command');
      }
      return data;
    },
  });
}
