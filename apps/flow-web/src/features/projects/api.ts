/**
 * Projects feature — typed queries and mutations backed by the SDK.
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

export type Project = components['schemas']['Project'];
export type ProjectMember = components['schemas']['ProjectMember'];
export type CreateProjectInput = components['schemas']['CreateProjectBody'];
export type PatchProjectInput = components['schemas']['PatchProjectBody'];
export type AddProjectMemberInput = components['schemas']['AddProjectMemberBody'];

export type ProjectRole = 'lead' | 'editor' | 'commenter' | 'viewer';

/** Query key factory for the projects feature. */
export const projectsKeys = {
  all: ['projects'] as const,
  list: (workspaceId: string) => [...projectsKeys.all, 'list', workspaceId] as const,
  detail: (id: string) => [...projectsKeys.all, 'detail', id] as const,
  members: (id: string) => [...projectsKeys.all, 'detail', id, 'members'] as const,
  dependencies: (id: string) => [...projectsKeys.all, 'detail', id, 'dependencies'] as const,
};

export type ProjectDependencyEdge = components['schemas']['ProjectDependencyEdge'];

/** Lightweight error thrown when the SDK returns an error envelope. */
export class ProjectApiError extends Error {
  readonly code: string | undefined;
  constructor(code: string | undefined, message: string) {
    super(message);
    this.name = 'ProjectApiError';
    this.code = code;
  }
}

function extractCode(detail: string): string | undefined {
  const m = detail.match(/^([A-Z][A-Z0-9_.]+):/);
  return m ? m[1] : undefined;
}

function toError(err: unknown, fallback: string): ProjectApiError {
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
    return new ProjectApiError(code, message);
  }
  return new ProjectApiError(undefined, fallback);
}

export function useProjectsQuery(workspaceId: string): UseSuspenseQueryResult<Project[]> {
  return useSuspenseQuery({
    queryKey: projectsKeys.list(workspaceId),
    queryFn: async (): Promise<Project[]> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/projects', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) throw toError(error, 'Failed to load projects');
      return data.projects ?? [];
    },
  });
}

export function useProjectQuery(id: string): UseSuspenseQueryResult<Project> {
  return useSuspenseQuery({
    queryKey: projectsKeys.detail(id || '__empty__'),
    queryFn: async (): Promise<Project> => {
      if (!id) throw new ProjectApiError(undefined, 'Missing project ID');
      const { data, error } = await sdk.GET('/projects/{prjId}', {
        params: { path: { prjId: id } },
      });
      if (error || !data) throw toError(error, 'Failed to load project');
      return data;
    },
    // Prevent retrying when ID is empty — the query will never succeed.
    retry: id ? 2 : false,
  });
}

export function useProjectDependenciesQuery(
  id: string,
): UseSuspenseQueryResult<ProjectDependencyEdge[]> {
  return useSuspenseQuery({
    queryKey: projectsKeys.dependencies(id),
    staleTime: 30_000,
    queryFn: async (): Promise<ProjectDependencyEdge[]> => {
      const { data, error } = await sdk.GET('/projects/{prjId}/dependencies', {
        params: { path: { prjId: id } },
      });
      if (error || !data) throw toError(error, 'Failed to load project dependencies');
      return data.edges ?? [];
    },
  });
}

/**
 * Given the raw edge list, compute for every task id how many `blocks`
 * edges point AT it whose source task is not yet done. This is the
 * "blocked by open" count that drives the lock badge on List / Board.
 */
export function computeBlockedByOpen(edges: readonly ProjectDependencyEdge[]): Map<string, number> {
  const m = new Map<string, number>();
  for (const e of edges) {
    if (e.kind !== 'blocks') continue;
    if (e.fromTaskDerivedState === 'done' || e.fromTaskDerivedState === 'cancelled') continue;
    m.set(e.toTaskId, (m.get(e.toTaskId) ?? 0) + 1);
  }
  return m;
}

export function useProjectMembersQuery(id: string): UseSuspenseQueryResult<ProjectMember[]> {
  return useSuspenseQuery({
    queryKey: projectsKeys.members(id),
    queryFn: async (): Promise<ProjectMember[]> => {
      const { data, error } = await sdk.GET('/projects/{prjId}/members', {
        params: { path: { prjId: id } },
      });
      if (error || !data) throw toError(error, 'Failed to load project members');
      return data.members ?? [];
    },
  });
}

export interface CreateProjectArgs {
  workspaceId: string;
  input: CreateProjectInput;
}

export function useCreateProject(): UseMutationResult<Project, ProjectApiError, CreateProjectArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ workspaceId, input }: CreateProjectArgs): Promise<Project> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/projects', {
        params: { path: { wsId: workspaceId } },
        body: input,
      });
      if (error || !data) throw toError(error, 'Failed to create project');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: projectsKeys.list(vars.workspaceId) });
    },
  });
}

export interface UpdateProjectArgs {
  id: string;
  patch: PatchProjectInput;
}

export function useUpdateProject(): UseMutationResult<Project, ProjectApiError, UpdateProjectArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, patch }: UpdateProjectArgs): Promise<Project> => {
      const { data, error } = await sdk.PATCH('/projects/{prjId}', {
        params: { path: { prjId: id } },
        body: patch,
      });
      if (error || !data) throw toError(error, 'Failed to update project');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: projectsKeys.detail(vars.id) });
      void qc.invalidateQueries({ queryKey: projectsKeys.all });
    },
  });
}

export function useDisableProject(): UseMutationResult<void, ProjectApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string): Promise<void> => {
      const { error } = await sdk.DELETE('/projects/{prjId}', {
        params: { path: { prjId: id } },
      });
      if (error) throw toError(error, 'Failed to disable project');
    },
    onSuccess: (_data, id) => {
      void qc.invalidateQueries({ queryKey: projectsKeys.detail(id) });
      void qc.invalidateQueries({ queryKey: projectsKeys.all });
    },
  });
}

export interface AddProjectMemberArgs {
  id: string;
  input: AddProjectMemberInput;
}

export function useAddProjectMember(): UseMutationResult<
  ProjectMember,
  ProjectApiError,
  AddProjectMemberArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, input }: AddProjectMemberArgs): Promise<ProjectMember> => {
      const { data, error } = await sdk.POST('/projects/{prjId}/members', {
        params: { path: { prjId: id } },
        body: input,
      });
      if (error || !data) throw toError(error, 'Failed to add project member');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: projectsKeys.members(vars.id) });
    },
  });
}
