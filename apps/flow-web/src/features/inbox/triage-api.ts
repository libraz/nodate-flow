/**
 * Inbox triage mutation. Wraps `POST /workspaces/{wsId}/inbox/triage`,
 * pushes returned suggestions into the shared suggestions store, and
 * returns them to the caller for inline rendering.
 */

import { type UseMutationResult, useMutation } from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';
import { type Suggestion, suggestionsStore } from '../ai-suggestions/store';
import { InboxApiError } from './api';

export interface InboxTriageArgs {
  /** Optional cap on items to score. Backend default 20, max 50. */
  limit?: number;
}

function toError(err: unknown, fallback: string): InboxApiError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    return new InboxApiError(code, message);
  }
  return new InboxApiError(undefined, fallback);
}

/**
 * useInboxTriageMutation — kicks off an AI triage pass for the given
 * workspace. On success the returned suggestions are also pushed into
 * the shared `suggestionsStore` so the Glass Dock picks them up.
 */
export function useInboxTriageMutation(
  workspaceId: string,
): UseMutationResult<Suggestion[], InboxApiError, InboxTriageArgs | undefined> {
  return useMutation({
    mutationFn: async (args: InboxTriageArgs | undefined): Promise<Suggestion[]> => {
      const body: InboxTriageArgs = args ?? {};
      const { data, error } = await sdk.POST('/workspaces/{wsId}/inbox/triage', {
        params: { path: { wsId: workspaceId } },
        body,
      });
      if (error || !data) throw toError(error, 'Failed to triage inbox');
      return data.suggestions ?? [];
    },
    onSuccess: (suggestions) => {
      suggestionsStore.getState().pushSuggestions(suggestions);
    },
  });
}
