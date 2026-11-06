/**
 * Inbox triage mutation. Wraps `POST /workspaces/{wsId}/inbox/triage`,
 * pushes returned suggestions into the shared suggestions store, and
 * returns them to the caller for inline rendering.
 */

import { type UseMutationResult, useMutation } from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import { ApiError } from '../../lib/api-error';
import { fetchAiSuggestions, SuggestionActorChangedError } from '../ai-suggestions/api';
import { type Suggestion, suggestionsStore } from '../ai-suggestions/store';
import { authStore, selectUser, useAuth } from '../auth/auth-store';

export { ApiError as InboxApiError, SuggestionActorChangedError };

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
): UseMutationResult<
  Suggestion[],
  ApiError | SuggestionActorChangedError,
  InboxTriageArgs | undefined
> {
  const actorId = useAuth(selectUser)?.id ?? '';
  return useMutation({
    mutationFn: async (args: InboxTriageArgs | undefined): Promise<Suggestion[]> => {
      if (!workspaceId || !actorId || authStore.getState().user?.id !== actorId) {
        throw new SuggestionActorChangedError();
      }
      const body: InboxTriageArgs = args ?? {};
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/inbox/triage', {
            params: { path: { wsId: workspaceId } },
            body,
          }),
        'Failed to triage inbox',
      );
      const triagedItemIDs = new Set(
        (data.suggestions ?? []).map((suggestion) => suggestion.inboxItemId),
      );
      if (authStore.getState().user?.id !== actorId) {
        throw new SuggestionActorChangedError();
      }
      const persistedSuggestions = await fetchAiSuggestions(workspaceId);
      if (authStore.getState().user?.id !== actorId) {
        throw new SuggestionActorChangedError();
      }
      return persistedSuggestions
        .filter((suggestion) => triagedItemIDs.has(suggestion.inboxItemId))
        .map((suggestion) => ({ ...suggestion, workspaceId, actorId }));
    },
    onSuccess: (suggestions) => {
      suggestionsStore.getState().pushSuggestions(suggestions);
    },
  });
}
