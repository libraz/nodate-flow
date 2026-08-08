/**
 * Workspaces feature — typed queries and mutations backed by the SDK.
 *
 * All hooks are suspense-ready where applicable and participate in the
 * shared QueryClient (throwOnError, route-level ErrorBoundary).
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { authSdk as sdk } from '../../lib/sdk';

export type Workspace = components['schemas']['Workspace'];
export type WorkspaceMember = components['schemas']['WorkspaceMember'];
export type WorkspaceUserSummary = components['schemas']['WorkspaceUserSummary'];
export type CreateWorkspaceInput = components['schemas']['CreateWorkspaceInputBody'];
export type PatchWorkspaceInput = components['schemas']['WorkspacePatchWorkspaceInputBody'];
export type AddMemberInput = components['schemas']['AddWorkspaceMemberInputBody'];
export type UpdateMemberRoleInput = components['schemas']['UpdateWorkspaceMemberRoleInputBody'];
/**
 * Result of {@link useDeleteWorkspace}. Mirrors the destructive-delete
 * envelope returned by `DELETE /workspaces/{wsId}`: `deleted` is `false`
 * when the workspace was already gone (idempotent), and the storage
 * counters surface the MinIO sweep outcome for the caller's toast.
 */
export type DeleteWorkspaceResult = components['schemas']['DeleteWorkspaceOutputBody'];

/** Query key factory for the workspaces feature. */
export const workspacesKeys = {
  all: ['workspaces'] as const,
  list: () => [...workspacesKeys.all, 'list'] as const,
  detail: (id: string) => [...workspacesKeys.all, 'detail', id] as const,
  members: (id: string) => [...workspacesKeys.all, 'detail', id, 'members'] as const,
  users: (id: string) => [...workspacesKeys.all, 'detail', id, 'users'] as const,
};

import { ApiError } from '../../lib/api-error';

export { ApiError as WorkspaceApiError };

function extractCode(detail: string): string | undefined {
  const m = detail.match(/^([A-Z][A-Z0-9_.]+):/);
  return m ? m[1] : undefined;
}

function toError(err: unknown, fallback: string): ApiError {
  if (err && typeof err === 'object') {
    const obj = err as {
      detail?: unknown;
      title?: unknown;
      type?: unknown;
      code?: unknown;
      message?: unknown;
    };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      (typeof obj.message === 'string' && obj.message) ||
      fallback;
    // The backend emits two error envelope shapes today. The newer
    // handlers use RFC 7807 (`{type, title, detail, status}`) where
    // `type` carries the error code. The ACL middleware that raises
    // WS.WORKSPACE.NOT_FOUND still uses the older `{code, message}`
    // shape. Accept both so route-level fallbacks can branch on code.
    const code =
      (typeof obj.code === 'string' && obj.code) ||
      (typeof obj.type === 'string' && obj.type) ||
      (typeof obj.detail === 'string' && extractCode(obj.detail)) ||
      undefined;
    return new ApiError(code, message);
  }
  return new ApiError(undefined, fallback);
}

export function useWorkspacesQuery(): UseSuspenseQueryResult<Workspace[]> {
  return useSuspenseQuery({
    queryKey: workspacesKeys.list(),
    queryFn: async (): Promise<Workspace[]> => {
      const { data, error } = await sdk.GET('/workspaces', {});
      if (error || !data) throw toError(error, 'Failed to load workspaces');
      return data.workspaces ?? [];
    },
  });
}

export function useWorkspaceQuery(id: string): UseSuspenseQueryResult<Workspace> {
  return useSuspenseQuery({
    queryKey: workspacesKeys.detail(id),
    queryFn: async (): Promise<Workspace> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}', {
        params: { path: { wsId: id } },
      });
      if (error || !data) throw toError(error, 'Failed to load workspace');
      return data;
    },
  });
}

export function useWorkspaceMembersQuery(id: string): UseSuspenseQueryResult<WorkspaceMember[]> {
  return useSuspenseQuery({
    queryKey: workspacesKeys.members(id),
    queryFn: async (): Promise<WorkspaceMember[]> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/members', {
        params: { path: { wsId: id } },
      });
      if (error || !data) throw toError(error, 'Failed to load workspace members');
      return data.members ?? [];
    },
  });
}

/**
 * Lists user summaries for actor pickers (timeline filter, assignee, etc.).
 * Backed by GET /workspaces/{wsId}/users which is gated by the
 * workspace-member ACL middleware on the API side.
 */
export function useWorkspaceUsersQuery(
  workspaceId: string,
): UseSuspenseQueryResult<WorkspaceUserSummary[]> {
  return useSuspenseQuery({
    queryKey: workspacesKeys.users(workspaceId),
    queryFn: async (): Promise<WorkspaceUserSummary[]> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/users', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) throw toError(error, 'Failed to load workspace users');
      return data.users ?? [];
    },
  });
}

export function useCreateWorkspace(): UseMutationResult<Workspace, ApiError, CreateWorkspaceInput> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: CreateWorkspaceInput): Promise<Workspace> => {
      const { data, error } = await sdk.POST('/workspaces', { body: input });
      if (error || !data) throw toError(error, 'Failed to create workspace');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: workspacesKeys.list() });
    },
  });
}

export interface UpdateWorkspaceArgs {
  id: string;
  patch: PatchWorkspaceInput;
}

export function useUpdateWorkspace(): UseMutationResult<Workspace, ApiError, UpdateWorkspaceArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, patch }: UpdateWorkspaceArgs): Promise<Workspace> => {
      const { data, error } = await sdk.PATCH('/workspaces/{wsId}', {
        params: { path: { wsId: id } },
        body: patch,
      });
      if (error || !data) throw toError(error, 'Failed to update workspace');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: workspacesKeys.list() });
      void qc.invalidateQueries({ queryKey: workspacesKeys.detail(vars.id) });
    },
  });
}

export interface DeleteWorkspaceArgs {
  wsId: string;
  /**
   * Must be `true`. The API rejects missing/false confirmation with a
   * 400 `WORKSPACE.DELETE.CONFIRM_REQUIRED`. Plumbed through the args
   * (rather than hardcoded) so the modal owns the gating logic and the
   * mutation hook stays a transport-only concern.
   */
  confirm: boolean;
}

/**
 * DELETE /workspaces/{wsId} — destructive immediate workspace delete by
 * the owner. Sweeps every MinIO blob owned by the workspace, then
 * issues a CASCADE-anchored hard DELETE on the workspaces row and
 * every dependent member, project, task, event, and attachment. The
 * server requires `{ confirm: true }`; missing/false yields a 400
 * `WORKSPACE.DELETE.CONFIRM_REQUIRED`.
 *
 * On success the mutation invalidates the workspaces list and removes
 * — rather than invalidates — the deleted workspace's detail cache so
 * any still-mounted suspense consumers do not refetch a 404.
 */
export function useDeleteWorkspace(): UseMutationResult<
  DeleteWorkspaceResult,
  ApiError,
  DeleteWorkspaceArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ wsId, confirm }: DeleteWorkspaceArgs): Promise<DeleteWorkspaceResult> => {
      const { data, error } = await sdk.DELETE('/workspaces/{wsId}', {
        params: { path: { wsId } },
        body: { confirm },
      });
      if (error || !data) throw toError(error, 'Failed to delete workspace');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: workspacesKeys.list() });
      // Remove — not invalidate — the deleted workspace's cache. The
      // subtree of the deleted workspace will 404 on refetch, and any
      // detail/members queries still mounted would otherwise retry
      // three times and log to the console before the navigating
      // consumer unmounts.
      qc.removeQueries({ queryKey: workspacesKeys.detail(vars.wsId) });
      qc.removeQueries({ queryKey: workspacesKeys.members(vars.wsId) });
      qc.removeQueries({ queryKey: workspacesKeys.users(vars.wsId) });
    },
  });
}

export interface AddMemberArgs {
  id: string;
  input: AddMemberInput;
}

export function useAddMember(): UseMutationResult<WorkspaceMember, ApiError, AddMemberArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, input }: AddMemberArgs): Promise<WorkspaceMember> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/members', {
        params: { path: { wsId: id } },
        body: input,
      });
      if (error || !data) throw toError(error, 'Failed to add member');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: workspacesKeys.members(vars.id) });
      void qc.invalidateQueries({ queryKey: workspacesKeys.users(vars.id) });
    },
  });
}

export interface UpdateMemberRoleArgs {
  wsId: string;
  userId: string;
  role: UpdateMemberRoleInput['role'];
}

/** PATCH /workspaces/{wsId}/members/{userId} — change a member's role. */
export function useUpdateMemberRole(): UseMutationResult<
  WorkspaceMember,
  ApiError,
  UpdateMemberRoleArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ wsId, userId, role }: UpdateMemberRoleArgs): Promise<WorkspaceMember> => {
      const { data, error } = await sdk.PATCH('/workspaces/{wsId}/members/{userId}', {
        params: { path: { wsId, userId } },
        body: { role },
      });
      if (error || !data) throw toError(error, 'Failed to update member role');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: workspacesKeys.members(vars.wsId) });
      void qc.invalidateQueries({ queryKey: workspacesKeys.users(vars.wsId) });
    },
  });
}

/** DELETE /workspaces/{wsId}/members/{userId} — remove a member. */
export interface RemoveMemberArgs {
  wsId: string;
  userId: string;
}

export function useRemoveMember(): UseMutationResult<void, ApiError, RemoveMemberArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ wsId, userId }: RemoveMemberArgs): Promise<void> => {
      const { error } = await sdk.DELETE('/workspaces/{wsId}/members/{userId}', {
        params: { path: { wsId, userId } },
      });
      if (error) throw toError(error, 'Failed to remove member');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: workspacesKeys.members(vars.wsId) });
      void qc.invalidateQueries({ queryKey: workspacesKeys.users(vars.wsId) });
    },
  });
}
