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

import { sdk } from '../../lib/sdk';

export type Workspace = components['schemas']['Workspace'];
export type WorkspaceMember = components['schemas']['WorkspaceMember'];
export type WorkspaceUserSummary = components['schemas']['WorkspaceUserSummary'];
export type CreateWorkspaceInput = components['schemas']['CreateWorkspaceInputBody'];
export type PatchWorkspaceInput = components['schemas']['PatchWorkspaceInputBody'];
export type InviteMemberInput = components['schemas']['AddWorkspaceMemberInputBody'];

/** Query key factory for the workspaces feature. */
export const workspacesKeys = {
  all: ['workspaces'] as const,
  list: () => [...workspacesKeys.all, 'list'] as const,
  detail: (id: string) => [...workspacesKeys.all, 'detail', id] as const,
  members: (id: string) => [...workspacesKeys.all, 'detail', id, 'members'] as const,
  users: (id: string) => [...workspacesKeys.all, 'detail', id, 'users'] as const,
};

/** Lightweight error thrown when the SDK returns an error envelope. */
export class WorkspaceApiError extends Error {
  readonly code: string | undefined;
  constructor(code: string | undefined, message: string) {
    super(message);
    this.name = 'WorkspaceApiError';
    this.code = code;
  }
}

function extractCode(detail: string): string | undefined {
  const m = detail.match(/^([A-Z][A-Z0-9_.]+):/);
  return m ? m[1] : undefined;
}

function toError(err: unknown, fallback: string): WorkspaceApiError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code =
      (typeof obj.type === 'string' && obj.type) ||
      (typeof obj.detail === 'string' && extractCode(obj.detail)) ||
      undefined;
    return new WorkspaceApiError(code, message);
  }
  return new WorkspaceApiError(undefined, fallback);
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

export function useCreateWorkspace(): UseMutationResult<
  Workspace,
  WorkspaceApiError,
  CreateWorkspaceInput
> {
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

export function useUpdateWorkspace(): UseMutationResult<
  Workspace,
  WorkspaceApiError,
  UpdateWorkspaceArgs
> {
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

export function useDisableWorkspace(): UseMutationResult<void, WorkspaceApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string): Promise<void> => {
      const { error } = await sdk.DELETE('/workspaces/{wsId}', {
        params: { path: { wsId: id } },
      });
      if (error) throw toError(error, 'Failed to disable workspace');
    },
    onSuccess: (_data, id) => {
      void qc.invalidateQueries({ queryKey: workspacesKeys.list() });
      void qc.invalidateQueries({ queryKey: workspacesKeys.detail(id) });
    },
  });
}

export interface AddMemberArgs {
  id: string;
  input: InviteMemberInput;
}

export function useAddMember(): UseMutationResult<
  WorkspaceMember,
  WorkspaceApiError,
  AddMemberArgs
> {
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
    },
  });
}
