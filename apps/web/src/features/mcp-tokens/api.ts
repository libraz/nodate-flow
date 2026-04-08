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

import { sdk } from '../../lib/sdk';

export type McpTokenSummary = components['schemas']['McpTokenSummary'];
export type CreateMcpTokenInput = components['schemas']['CreateMcpTokenInputBody'];
export type CreateMcpTokenOutput = components['schemas']['CreateMcpTokenOutputBody'];

/** Query key factory for the MCP tokens feature. */
export const mcpTokensKeys = {
  all: ['mcp-tokens'] as const,
  list: (workspaceId: string) => [...mcpTokensKeys.all, 'list', workspaceId] as const,
};

/** Lightweight error thrown when the SDK returns an error envelope. */
export class McpTokenApiError extends Error {
  readonly code: string | undefined;
  constructor(code: string | undefined, message: string) {
    super(message);
    this.name = 'McpTokenApiError';
    this.code = code;
  }
}

function toError(err: unknown, fallback: string): McpTokenApiError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    return new McpTokenApiError(code, message);
  }
  return new McpTokenApiError(undefined, fallback);
}

/** Suspense query: list MCP tokens for the given workspace. */
export function useMcpTokensQuery(workspaceId: string): UseSuspenseQueryResult<McpTokenSummary[]> {
  return useSuspenseQuery({
    queryKey: mcpTokensKeys.list(workspaceId),
    queryFn: async (): Promise<McpTokenSummary[]> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/me/mcp-tokens', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) throw toError(error, 'Failed to load MCP tokens');
      return data.tokens ?? [];
    },
  });
}

/** Mutation: create a new MCP token. The plaintext is in the result only. */
export function useCreateMcpToken(
  workspaceId: string,
): UseMutationResult<CreateMcpTokenOutput, McpTokenApiError, CreateMcpTokenInput> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: CreateMcpTokenInput): Promise<CreateMcpTokenOutput> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/me/mcp-tokens', {
        params: { path: { wsId: workspaceId } },
        body: input,
      });
      if (error || !data) throw toError(error, 'Failed to create MCP token');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: mcpTokensKeys.list(workspaceId) });
    },
  });
}

/** Mutation: revoke (delete) an MCP token by id. */
export function useRevokeMcpToken(
  workspaceId: string,
): UseMutationResult<void, McpTokenApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (tokenId: string): Promise<void> => {
      const { error } = await sdk.DELETE('/workspaces/{wsId}/me/mcp-tokens/{tokenId}', {
        params: { path: { wsId: workspaceId, tokenId } },
      });
      if (error) throw toError(error, 'Failed to revoke MCP token');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: mcpTokensKeys.list(workspaceId) });
    },
  });
}
