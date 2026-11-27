/**
 * Caller's role in the current workspace. Non-suspense so a slow or failed
 * role lookup never takes a screen down; it shares the
 * `workspacesKeys.detail` cache entry with `useWorkspaceQuery`.
 */

import { useQuery } from '@tanstack/react-query';

import { fetchWorkspace, workspacesKeys } from '../features/workspaces/api';
import { useCurrentWorkspaceId } from './use-current-workspace';

export type WorkspaceRole = 'owner' | 'admin' | 'member' | 'guest';

const ROLES: readonly string[] = ['owner', 'admin', 'member', 'guest'];

export interface WorkspaceRoleState {
  /** Null while loading, on error, or with no current workspace. */
  role: WorkspaceRole | null;
  /** True until the role lookup settles (success or error). */
  isPending: boolean;
}

/** Role plus whether the lookup is still in flight, for callers that must wait before routing on it. */
export function useCurrentWorkspaceRoleState(): WorkspaceRoleState {
  const id = useCurrentWorkspaceId();
  const { data, isPending } = useQuery({
    queryKey: workspacesKeys.detail(id ?? ''),
    queryFn: () => fetchWorkspace(id ?? ''),
    enabled: Boolean(id),
    throwOnError: false,
  });
  const role = data?.role;
  return {
    role: role !== undefined && ROLES.includes(role) ? (role as WorkspaceRole) : null,
    isPending: Boolean(id) && isPending,
  };
}

/** Null while loading, on error, or with no current workspace. */
export function useCurrentWorkspaceRole(): WorkspaceRole | null {
  return useCurrentWorkspaceRoleState().role;
}

export function isWorkspaceAdmin(role: WorkspaceRole | null): boolean {
  return role === 'owner' || role === 'admin';
}
