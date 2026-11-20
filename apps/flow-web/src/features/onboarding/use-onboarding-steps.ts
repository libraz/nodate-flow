/**
 * Completion state for the getting-started checklist, derived from the
 * queries the product already runs. Every query here is non-suspense: a
 * failed lookup (an invite list is admin-only and may answer 403) leaves
 * its step incomplete instead of taking the home page down.
 */

import { useQuery } from '@tanstack/react-query';

import { apiRequest, authApiRequest } from '../../lib/api';
import { useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import { isWorkspaceAdmin, useCurrentWorkspaceRole } from '../../lib/use-current-workspace-role';
import { type Project, projectsKeys } from '../projects/api';
import { tasksKeys } from '../tasks/api';
import { fetchWorkspaceMembers, useWorkspacesQuery, workspacesKeys } from '../workspaces/api';
import { inviteKeys, type WorkspaceInvite } from '../workspaces/invite-api';

export type OnboardingStepId = 'workspace' | 'project' | 'task' | 'invite';

export interface OnboardingStep {
  id: OnboardingStepId;
  done: boolean;
}

export interface OnboardingState {
  /** Whether the viewer is the audience for the checklist at all. */
  eligible: boolean;
  workspaceId: string | null;
  steps: OnboardingStep[];
  doneCount: number;
}

function isLiveInvite(invite: WorkspaceInvite): boolean {
  const notExpired = invite.expiresAt === undefined || invite.expiresAt * 1000 > Date.now();
  const hasUses = invite.maxUses === null || invite.useCount < invite.maxUses;
  return notExpired && hasUses;
}

export function useOnboardingSteps(): OnboardingState {
  const { data: workspaces } = useWorkspacesQuery();
  const workspaceId = useCurrentWorkspaceId();
  const role = useCurrentWorkspaceRole();
  const isAdmin = isWorkspaceAdmin(role);
  const hasWorkspace = workspaces.length > 0;

  const wsId = workspaceId ?? '';
  const projectsQuery = useQuery({
    queryKey: projectsKeys.list(wsId),
    enabled: hasWorkspace && isAdmin,
    queryFn: async (): Promise<Project[]> => {
      const data = await apiRequest(
        (client) => client.GET('/workspaces/{wsId}/projects', { params: { path: { wsId } } }),
        'Failed to load projects',
      );
      return data.projects ?? [];
    },
  });
  const firstProjectId = projectsQuery.data?.find((p) => !p.isArchived)?.id ?? null;
  const projectDone = firstProjectId !== null;

  const tasksQuery = useQuery({
    queryKey: [...tasksKeys.all, 'list', firstProjectId ?? '', 'onboarding-probe'] as const,
    enabled: projectDone,
    select: (count: number) => count > 0,
    queryFn: async (): Promise<number> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/tasks', {
            params: { query: { projectId: firstProjectId ?? '', limit: 1, offset: 0 } },
          }),
        'Failed to load tasks',
      );
      return data.tasks?.length ?? 0;
    },
  });

  const membersQuery = useQuery({
    queryKey: workspacesKeys.members(wsId),
    enabled: hasWorkspace && isAdmin,
    select: (members) => members.length >= 2,
    queryFn: () => fetchWorkspaceMembers(wsId),
  });
  const invitesQuery = useQuery({
    queryKey: inviteKeys.list(wsId),
    enabled: hasWorkspace && isAdmin,
    select: (invites: WorkspaceInvite[]) => invites.some(isLiveInvite),
    queryFn: async (): Promise<WorkspaceInvite[]> => {
      const data = await authApiRequest(
        (client) => client.GET('/workspaces/{wsId}/invites', { params: { path: { wsId } } }),
        'Failed to load invites',
      );
      return data.invites ?? [];
    },
  });

  const steps: OnboardingStep[] = [
    { id: 'workspace', done: hasWorkspace },
    { id: 'project', done: projectDone },
    { id: 'task', done: projectDone && tasksQuery.data === true },
    { id: 'invite', done: membersQuery.data === true || invitesQuery.data === true },
  ];

  return {
    eligible: !hasWorkspace || isAdmin,
    workspaceId: hasWorkspace ? workspaceId : null,
    steps,
    doneCount: steps.filter((s) => s.done).length,
  };
}
