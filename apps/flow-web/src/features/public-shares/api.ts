/**
 * Public shares feature — typed queries and mutations against the
 * /workspaces/{wsId}/public-shares family of endpoints.
 *
 * Security:
 * - The plaintext URL token is returned only by the create and rotate
 *   mutations. It is never written to a query cache; callers must keep
 *   the value in component-local state, show it to the user once, and
 *   clear it as soon as the dialog closes.
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
import { ApiError } from '../../lib/api-error';

export type PublicShare = components['schemas']['PublicShareResponse'];
export type PublicShareWithToken =
  | components['schemas']['PublicShareCreateResponse']
  | components['schemas']['PublicShareRotateResponse'];
export type ShareEvent = components['schemas']['ShareEventResponse'];
/**
 * One occurrence of a published series whose replacement row is not on the
 * share, so the public page still shows the start it moved away from.
 */
export type ShareOverrideWarning = components['schemas']['ShareOverrideWarning'];
export type ShareDetail = components['schemas']['GetPublicShareOutputBody'];
export type CrossCalendarEvent = components['schemas']['CrossCalendarEventResponse'];
export type CreatePublicShareInput = components['schemas']['CreatePublicShareInputBody'];
export type PatchPublicShareInput = components['schemas']['PatchPublicShareInputBody'];

export { ApiError as PublicShareApiError };

export const publicSharesKeys = {
  all: ['public-shares'] as const,
  list: (workspaceId: string) => [...publicSharesKeys.all, workspaceId] as const,
  detail: (workspaceId: string, shareId: string) =>
    [...publicSharesKeys.all, workspaceId, shareId] as const,
  candidates: (workspaceId: string, start: string, end: string) =>
    [...publicSharesKeys.all, workspaceId, 'candidates', start, end] as const,
};

/** Suspense query: list public share pages for the given workspace. */
export function usePublicSharesQuery(workspaceId: string): UseSuspenseQueryResult<PublicShare[]> {
  return useSuspenseQuery({
    queryKey: publicSharesKeys.list(workspaceId),
    queryFn: async (): Promise<PublicShare[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/public-shares', {
            params: { path: { wsId: workspaceId } },
          }),
        'Failed to load public share pages',
      );
      return data.shares ?? [];
    },
  });
}

/**
 * Mutation: create a new public share page. The plaintext URL token is
 * inside the returned `share.token` and must be captured on this result —
 * it will never be returned again by the server.
 */
export function useCreatePublicShare(
  workspaceId: string,
): UseMutationResult<PublicShareWithToken, ApiError, CreatePublicShareInput> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: CreatePublicShareInput): Promise<PublicShareWithToken> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/public-shares', {
            params: { path: { wsId: workspaceId } },
            body: input,
          }),
        'Failed to create public share page',
      );
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: publicSharesKeys.list(workspaceId) });
    },
  });
}

/** Mutation: delete a public share page (admin/owner only server-side). */
export function useDeletePublicShare(
  workspaceId: string,
): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (shareId: string): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/public-shares/{shareId}', {
            params: { path: { wsId: workspaceId, shareId } },
          }),
        'Failed to delete public share page',
      );
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: publicSharesKeys.list(workspaceId) });
    },
  });
}

/**
 * Mutation: rotate the URL token. The new plaintext is in `share.token`
 * on the result; the previous URL immediately becomes invalid.
 */
export function useRotatePublicShareToken(
  workspaceId: string,
): UseMutationResult<PublicShareWithToken, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (shareId: string): Promise<PublicShareWithToken> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/public-shares/{shareId}/rotate', {
            params: { path: { wsId: workspaceId, shareId } },
          }),
        'Failed to rotate share token',
      );
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: publicSharesKeys.list(workspaceId) });
    },
  });
}

/** Suspense query: fetch one share plus its attached events, for the editor page. */
export function usePublicShareDetailQuery(
  workspaceId: string,
  shareId: string,
): UseSuspenseQueryResult<ShareDetail> {
  return useSuspenseQuery({
    queryKey: publicSharesKeys.detail(workspaceId, shareId),
    queryFn: async (): Promise<ShareDetail> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/public-shares/{shareId}', {
            params: { path: { wsId: workspaceId, shareId } },
          }),
        'Failed to load public share page',
      );
      return data;
    },
  });
}

/**
 * Suspense query: list workspace events in a time range for the picker.
 *
 * Backed by `/workspaces/{wsId}/calendar-events?start=&end=` — a cross-calendar
 * range query. The picker filters confidential events client-side; the server
 * rejects them on attach as a final safety gate.
 */
export function useWorkspaceCalendarEventsQuery(
  workspaceId: string,
  startIso: string,
  endIso: string,
): UseSuspenseQueryResult<CrossCalendarEvent[]> {
  return useSuspenseQuery({
    queryKey: publicSharesKeys.candidates(workspaceId, startIso, endIso),
    queryFn: async (): Promise<CrossCalendarEvent[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/calendar-events', {
            params: {
              path: { wsId: workspaceId },
              query: { start: startIso, end: endIso },
            },
          }),
        'Failed to load workspace events',
      );
      return data.events ?? [];
    },
  });
}

/**
 * Mutation: attach one or more events to a share. The server returns
 * `{ attached, skipped }` — skipped covers rows that were already linked or
 * rejected (e.g. confidential events). Invalidates the detail query.
 */
export function useAttachEventsToShare(
  workspaceId: string,
  shareId: string,
): UseMutationResult<components['schemas']['AttachEventsToShareOutputBody'], ApiError, string[]> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (
      eventIds: string[],
    ): Promise<components['schemas']['AttachEventsToShareOutputBody']> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/public-shares/{shareId}/events', {
            params: { path: { wsId: workspaceId, shareId } },
            body: { eventIds },
          }),
        'Failed to attach events',
      );
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: publicSharesKeys.detail(workspaceId, shareId) });
      void qc.invalidateQueries({ queryKey: publicSharesKeys.list(workspaceId) });
    },
  });
}

/**
 * Mutation: reorder the events attached to a share. The body must be the
 * complete new ordering, identified by the `linkId` (share-event link
 * public ID) of every row currently attached — a strict permutation.
 * Invalidates the detail query on success so the server order re-hydrates.
 */
export function useReorderShareEvents(
  workspaceId: string,
  shareId: string,
): UseMutationResult<components['schemas']['ReorderShareEventsOutputBody'], ApiError, string[]> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (
      linkPublicIds: string[],
    ): Promise<components['schemas']['ReorderShareEventsOutputBody']> => {
      const data = await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/public-shares/{shareId}/events/reorder', {
            params: { path: { wsId: workspaceId, shareId } },
            body: { linkPublicIds },
          }),
        'Failed to reorder events',
      );
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: publicSharesKeys.detail(workspaceId, shareId) });
    },
    onError: () => {
      // On failure, invalidate so the UI reverts to the server's truth.
      void qc.invalidateQueries({ queryKey: publicSharesKeys.detail(workspaceId, shareId) });
    },
  });
}

/** Mutation: detach a single event from a share. */
export function useDetachEventFromShare(
  workspaceId: string,
  shareId: string,
): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (eventId: string): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/public-shares/{shareId}/events/{evtId}', {
            params: { path: { wsId: workspaceId, shareId, evtId: eventId } },
          }),
        'Failed to detach event',
      );
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: publicSharesKeys.detail(workspaceId, shareId) });
      void qc.invalidateQueries({ queryKey: publicSharesKeys.list(workspaceId) });
    },
  });
}
