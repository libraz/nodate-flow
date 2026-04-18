/**
 * NL query feature — POST /workspaces/{wsId}/ai/compile-lens.
 *
 * Returns a validated {@link Lens} compiled from a natural language
 * prompt (ADR 0004). Failures surface as AI.NL_QUERY.UNPARSEABLE so the
 * glass dock can render a "rephrase" toast.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseMutationResult, useMutation } from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

/** Validated Lens mirrored from the SDK. */
export type Lens = components['schemas']['Lens'];

/** Result of a successful compile-lens call. */
export interface CompileLensResult {
  prompt: string;
  lens: Lens;
}

/** Error envelope for NL query failures. */
export class NlQueryError extends Error {
  readonly code: string | undefined;
  constructor(code: string | undefined, message: string) {
    super(message);
    this.name = 'NlQueryError';
    this.code = code;
  }
}

function toError(err: unknown, fallback: string): NlQueryError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    return new NlQueryError(code, message);
  }
  return new NlQueryError(undefined, fallback);
}

export interface CompileLensArgs {
  workspaceId: string;
  prompt: string;
}

/**
 * useCompileLens — mutation that calls POST /workspaces/{wsId}/ai/compile-lens.
 *
 * The backend validates against the closed NL→Lens grammar (ADR 0004) and
 * returns AI.NL_QUERY.UNPARSEABLE on failure; callers should render a
 * "rephrase" hint when {@link NlQueryError.code} contains that code.
 */
export function useCompileLens(): UseMutationResult<
  CompileLensResult,
  NlQueryError,
  CompileLensArgs
> {
  return useMutation<CompileLensResult, NlQueryError, CompileLensArgs>({
    mutationFn: async ({ workspaceId, prompt }): Promise<CompileLensResult> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/ai/compile-lens', {
        params: { path: { wsId: workspaceId } },
        body: { prompt },
      });
      if (error || !data) throw toError(error, 'Failed to compile prompt');
      return { prompt: data.prompt, lens: data.lens };
    },
  });
}
