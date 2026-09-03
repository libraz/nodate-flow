/**
 * Notification preferences — typed Suspense query and mutation for the
 * caller's own event-category by delivery-channel matrix
 * (`GET`/`PUT /workspaces/{wsId}/notification-preferences`).
 *
 * Preferences are stored per workspace, so every hook here takes a
 * workspace id; there is no cross-workspace form of the setting.
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
import type { ApiError } from '../../lib/api-error';

export type NotificationPreference = components['schemas']['NotificationPreferenceDTO'];

/** Query key factory for the notification preferences matrix. */
export const notificationPreferencesKeys = {
  all: ['notification-preferences'] as const,
  forWorkspace: (workspaceId: string) => [...notificationPreferencesKeys.all, workspaceId] as const,
};

/**
 * GET /workspaces/{wsId}/notification-preferences — the complete
 * matrix with the value fan-out applies to each cell, defaults
 * included.
 */
export function useNotificationPreferencesQuery(
  workspaceId: string,
): UseSuspenseQueryResult<NotificationPreference[]> {
  return useSuspenseQuery({
    queryKey: notificationPreferencesKeys.forWorkspace(workspaceId),
    queryFn: async (): Promise<NotificationPreference[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/notification-preferences', {
            params: { path: { wsId: workspaceId } },
          }),
        'Failed to load notification preferences',
      );
      return data.preferences ?? [];
    },
  });
}

/**
 * PUT /workspaces/{wsId}/notification-preferences — write the listed
 * cells and adopt the matrix the server returns.
 *
 * The response is written straight into the cache rather than merged
 * with the submitted cells: the server resolves defaults, so it is the
 * only party that knows what a cell ends up meaning.
 */
export function useUpdateNotificationPreferences(
  workspaceId: string,
): UseMutationResult<NotificationPreference[], ApiError, NotificationPreference[]> {
  const qc = useQueryClient();
  return useMutation<NotificationPreference[], ApiError, NotificationPreference[]>({
    throwOnError: false,
    mutationFn: async (
      preferences: NotificationPreference[],
    ): Promise<NotificationPreference[]> => {
      const data = await apiRequest(
        (client) =>
          client.PUT('/workspaces/{wsId}/notification-preferences', {
            params: { path: { wsId: workspaceId } },
            body: { preferences },
          }),
        'Failed to update notification preferences',
      );
      return data.preferences ?? [];
    },
    onSuccess: (data) => {
      qc.setQueryData<NotificationPreference[]>(
        notificationPreferencesKeys.forWorkspace(workspaceId),
        data,
      );
    },
  });
}
