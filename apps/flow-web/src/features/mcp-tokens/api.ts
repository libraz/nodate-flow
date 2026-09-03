/**
 * MCP tokens feature — typed queries and mutations backed by the SDK.
 *
 * Security:
 * - The plaintext bearer token is returned only by the create mutation result.
 * - It is never written to a query cache; callers must keep it in component
 *   local state and clear it as soon as possible.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { apiRequest } from '../../lib/api';

export type McpTokenSummary = components['schemas']['McpTokenSummary'];
export type CreateMcpTokenInput = components['schemas']['CreateMcpTokenInputBody'];
export type CreateMcpTokenOutput = components['schemas']['CreateMcpTokenOutputBody'];

/** Query key factory for the MCP tokens feature. */
export const mcpTokensKeys = {
  all: ['mcp-tokens'] as const,
  list: (workspaceId: string) => [...mcpTokensKeys.all, 'list', workspaceId] as const,
};

import { ApiError } from '../../lib/api-error';

export { ApiError as McpTokenApiError };

/** Suspense query: list MCP tokens for the given workspace. */
export function useMcpTokensQuery(workspaceId: string): UseSuspenseQueryResult<McpTokenSummary[]> {
  return useSuspenseQuery({
    queryKey: mcpTokensKeys.list(workspaceId),
    queryFn: async (): Promise<McpTokenSummary[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/me/mcp-tokens', {
            params: { path: { wsId: workspaceId } },
          }),
        'Failed to load MCP tokens',
      );
      return data.tokens ?? [];
    },
  });
}

/** Mutation: create a new MCP token. The plaintext is in the result only. */
export function useCreateMcpToken(
  workspaceId: string,
): UseMutationResult<CreateMcpTokenOutput, ApiError, CreateMcpTokenInput> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: CreateMcpTokenInput): Promise<CreateMcpTokenOutput> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/me/mcp-tokens', {
            params: { path: { wsId: workspaceId } },
            body: input,
          }),
        'Failed to create MCP token',
      );
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: mcpTokensKeys.list(workspaceId) });
    },
  });
}

/** Mutation: revoke (delete) an MCP token by id. */
export function useRevokeMcpToken(workspaceId: string): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (tokenId: string): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/me/mcp-tokens/{tokenId}', {
            params: { path: { wsId: workspaceId, tokenId } },
          }),
        'Failed to revoke MCP token',
      );
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: mcpTokensKeys.list(workspaceId) });
    },
  });
}
