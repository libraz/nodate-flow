/**
 * NL command feature — mutation hook for resolving natural language
 * commands via the AI resolve-command endpoint.
 *
 * Uses raw fetch (like notifications) because the SDK may not yet
 * include this endpoint.
 */

import { type UseMutationResult, useMutation } from '@tanstack/react-query';

import { apiBaseUrl } from '../../lib/sdk';
import { authStore } from '../auth/auth-store';

/** Result returned by POST /workspaces/{wsId}/ai/resolve-command. */
export interface ResolveCommandResult {
  tool: string;
  args: Record<string, unknown>;
  confidence: number;
}

function authHeaders(): HeadersInit {
  const token = authStore.getState().accessToken;
  // biome-ignore lint/style/useNamingConvention: HTTP header name
  return token ? { Authorization: `Bearer ${token}` } : {};
}

/** Resolves a natural language prompt into a tool invocation. */
export function useResolveCommand(
  wsId: string | null,
): UseMutationResult<ResolveCommandResult, Error, string> {
  return useMutation({
    mutationFn: async (prompt: string): Promise<ResolveCommandResult> => {
      if (!wsId) throw new Error('No workspace selected');
      const res = await fetch(`${apiBaseUrl}/workspaces/${wsId}/ai/resolve-command`, {
        method: 'POST',
        credentials: 'include',
        headers: {
          'Content-Type': 'application/json',
          ...authHeaders(),
        },
        body: JSON.stringify({ prompt }),
      });
      if (!res.ok) {
        throw new Error(`Failed to resolve command (${String(res.status)})`);
      }
      return (await res.json()) as ResolveCommandResult;
    },
  });
}
