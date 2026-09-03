/**
 * NL query feature — POST /workspaces/{wsId}/ai/compile-lens.
 *
 * Returns a validated {@link Lens} compiled from a natural language
 * prompt (ADR 0004). Failures surface as AI.NL_QUERY.UNPARSEABLE so the
 * glass dock can render a "rephrase" toast.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseMutationResult, useMutation } from '@tanstack/react-query';

import { apiRequest } from '../../lib/api';

/** Validated Lens mirrored from the SDK. */
export type Lens = components['schemas']['Lens'];

/** Result of a successful compile-lens call. */
export interface CompileLensResult {
  prompt: string;
  lens: Lens;
}

import { ApiError } from '../../lib/api-error';

export { ApiError as NlQueryError };

export interface CompileLensArgs {
  workspaceId: string;
  prompt: string;
}

/**
 * useCompileLens — mutation that calls POST /workspaces/{wsId}/ai/compile-lens.
 *
 * The backend validates against the closed NL→Lens grammar (ADR 0004) and
 * returns AI.NL_QUERY.UNPARSEABLE on failure; callers should render a
 * "rephrase" hint when {@link ApiError.code} contains that code.
 */
export function useCompileLens(): UseMutationResult<CompileLensResult, ApiError, CompileLensArgs> {
  return useMutation<CompileLensResult, ApiError, CompileLensArgs>({
    mutationFn: async ({ workspaceId, prompt }): Promise<CompileLensResult> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/ai/compile-lens', {
            params: { path: { wsId: workspaceId } },
            body: { prompt },
          }),
        'Failed to compile prompt',
      );
      return { prompt: data.prompt, lens: data.lens };
    },
  });
}
