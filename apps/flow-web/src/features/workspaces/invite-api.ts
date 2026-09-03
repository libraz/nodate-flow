/**
 * Workspace invite links — typed queries and mutations backed by the SDK.
 *
 * Covers create / list / revoke invite links, plus the public accept and
 * info endpoints for token-based invite flows.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { authApiRequest } from '../../lib/api';
import { ApiError } from '../../lib/api-error';
import { workspacesKeys } from './api';

export { ApiError as WorkspaceApiError };

export type WorkspaceInvite = components['schemas']['WorkspaceInvite'];
export type CreateInviteInput = components['schemas']['CreateWorkspaceInviteInputBody'];
export type InviteInfoOutput = components['schemas']['InviteInfoOutputBody'];
export type AcceptInviteOutput = components['schemas']['AcceptWorkspaceInviteOutputBody'];

/** Query key factory for workspace invites. */
export const inviteKeys = {
  all: (wsId: string) => [...workspacesKeys.all, 'detail', wsId, 'invites'] as const,
  list: (wsId: string) => [...inviteKeys.all(wsId), 'list'] as const,
  info: (token: string) => ['invites', 'info', token] as const,
};

/** POST /workspaces/{wsId}/invites — create an invite link. */
export interface CreateInviteArgs {
  wsId: string;
  input: CreateInviteInput;
}

export interface CreateInviteResult {
  invite: WorkspaceInvite;
  token: string;
}

export function useCreateInvite(): UseMutationResult<
  CreateInviteResult,
  ApiError,
  CreateInviteArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ wsId, input }: CreateInviteArgs): Promise<CreateInviteResult> => {
      const data = await authApiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/invites', {
            params: { path: { wsId } },
            body: input,
          }),
        'Failed to create invite',
      );
      return { invite: data.invite, token: data.token };
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: inviteKeys.list(vars.wsId) });
    },
  });
}

/** GET /workspaces/{wsId}/invites — list active invite links. */
export function useListInvitesQuery(wsId: string): UseSuspenseQueryResult<WorkspaceInvite[]> {
  return useSuspenseQuery({
    queryKey: inviteKeys.list(wsId),
    queryFn: async (): Promise<WorkspaceInvite[]> => {
      const data = await authApiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/invites', {
            params: { path: { wsId } },
          }),
        'Failed to load invites',
      );
      return data.invites ?? [];
    },
  });
}

/** DELETE /workspaces/{wsId}/invites/{inviteId} — revoke an invite link. */
export interface RevokeInviteArgs {
  wsId: string;
  inviteId: string;
}

export function useRevokeInvite(): UseMutationResult<void, ApiError, RevokeInviteArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ wsId, inviteId }: RevokeInviteArgs): Promise<void> => {
      await authApiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/invites/{inviteId}', {
            params: { path: { wsId, inviteId } },
          }),
        'Failed to revoke invite',
      );
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: inviteKeys.list(vars.wsId) });
    },
  });
}

/** POST /invites/{token}/accept — accept an invite link (authenticated). */
export function useAcceptInvite(): UseMutationResult<AcceptInviteOutput, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (token: string): Promise<AcceptInviteOutput> => {
      const data = await authApiRequest(
        (client) =>
          client.POST('/invites/{token}/accept', {
            params: { path: { token } },
          }),
        'Failed to accept invite',
      );
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: workspacesKeys.list() });
    },
  });
}

/**
 * inviteInfoQueryOptions — shared query options for the public invite-info
 * fetch. Exposed so route-level `beforeLoad` hooks can prefetch the same
 * cache entry the component reads via {@link useInviteInfoQuery}, keeping
 * the queryKey, queryFn, and error mapping in a single place.
 */
export function inviteInfoQueryOptions(token: string): {
  queryKey: ReturnType<typeof inviteKeys.info>;
  queryFn: () => Promise<InviteInfoOutput>;
} {
  return {
    queryKey: inviteKeys.info(token),
    queryFn: async (): Promise<InviteInfoOutput> => {
      const data = await authApiRequest(
        (client) =>
          client.GET('/invites/{token}/info', {
            params: { path: { token } },
          }),
        'Failed to load invite info',
      );
      return data;
    },
  };
}

/** GET /invites/{token}/info — public info about an invite (no auth required). */
export function useInviteInfoQuery(token: string): UseSuspenseQueryResult<InviteInfoOutput> {
  return useSuspenseQuery(inviteInfoQueryOptions(token));
}
