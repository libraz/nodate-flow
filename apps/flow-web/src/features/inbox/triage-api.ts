/**
 * Inbox triage mutation. Wraps `POST /workspaces/{wsId}/inbox/triage`,
 * pushes returned suggestions into the shared suggestions store, and
 * returns them to the caller for inline rendering.
 */

import { type UseMutationResult, useMutation } from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import { ApiError } from '../../lib/api-error';
import { type Suggestion, suggestionsStore } from '../ai-suggestions/store';

export { ApiError as InboxApiError };

export interface InboxTriageArgs {
  /** Optional cap on items to score. Backend default 20, max 50. */
  limit?: number;
}

/**
 * useInboxTriageMutation — kicks off an AI triage pass for the given
 * workspace. On success the returned suggestions are also pushed into
 * the shared `suggestionsStore` so the Glass Dock picks them up.
 */
export function useInboxTriageMutation(
  workspaceId: string,
): UseMutationResult<Suggestion[], ApiError, InboxTriageArgs | undefined> {
  return useMutation({
    mutationFn: async (args: InboxTriageArgs | undefined): Promise<Suggestion[]> => {
      const body: InboxTriageArgs = args ?? {};
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/inbox/triage', {
            params: { path: { wsId: workspaceId } },
            body,
          }),
        'Failed to triage inbox',
      );
      return data.suggestions ?? [];
    },
    onSuccess: (suggestions) => {
      suggestionsStore.getState().pushSuggestions(suggestions);
    },
  });
}
