/**
 * useCurrentWorkspaceId — derives the "active" workspace id from the
 * current URL, with a localStorage fallback so routes that are not
 * workspace-scoped (`/inbox`, `/today`, `/calendar`, `/pages`,
 * `/settings`) still have a workspace context for the sidebar project
 * tree, the AI dock, notifications, etc.
 *
 * Resolution order:
 *   1. `/workspaces/{id}/...` — direct URL match.
 *   2. `/projects/{pid}/...` — legacy URL, resolved via a non-suspense
 *      project fetch. Kept for transition; the canonical path is now
 *      workspace-scoped.
 *   3. `/tasks/{id}/...` — resolved via a non-suspense task fetch.
 *   4. `localStorage['nf.activeWsId']` — last visited workspace, gated
 *      by the current user's visible workspaces list so a stored id
 *      from a workspace the user no longer has access to does not
 *      leak.
 *
 * Whenever a non-null id is resolved from the URL we persist it to
 * `localStorage['nf.activeWsId']` so the next non-scoped route can
 * hydrate immediately without a round-trip.
 *
 * The `useActiveWorkspaceId` export is an alias that makes the intent
 * explicit at call sites; both hooks return the same value. Callers
 * that want the strict URL-derived id (no persistence fallback) should
 * read `useRouterState` directly.
 */

import { useQuery } from '@tanstack/react-query';
import { useRouterState } from '@tanstack/react-router';
import { useEffect, useMemo } from 'react';

import { projectsKeys } from '../features/projects/api';
import { workspacesKeys } from '../features/workspaces/api';
import { authSdk, sdk } from './sdk';

/** localStorage key used to persist the last-visited workspace id. */
const STORAGE_KEY = 'nf.activeWsId';

function readStoredWsId(): string | null {
  if (typeof window === 'undefined') return null;
  try {
    const v = window.localStorage.getItem(STORAGE_KEY);
    return v && v.length > 0 ? v : null;
  } catch {
    return null;
  }
}

function writeStoredWsId(id: string): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(STORAGE_KEY, id);
  } catch {
    /* quota / disabled storage — ignore */
  }
}

/**
 * Clear the persisted active workspace id. Call this from sign-out so
 * the next login does not restore the previous user's workspace as a
 * sidebar shortcut.
 */
export function clearActiveWorkspaceId(): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.removeItem(STORAGE_KEY);
  } catch {
    /* ignore */
  }
}

export function useCurrentWorkspaceId(): string | null {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const wsMatch = useMemo(() => /^\/workspaces\/([^/]+)(?:\/|$)/.exec(pathname), [pathname]);
  const projectMatch = useMemo(() => /^\/projects\/([^/]+)(?:\/|$)/.exec(pathname), [pathname]);
  const taskMatch = useMemo(() => /^\/tasks\/([^/]+)(?:\/|$)/.exec(pathname), [pathname]);
  const projectId = projectMatch ? (projectMatch[1] ?? null) : null;
  const taskId = taskMatch ? (taskMatch[1] ?? null) : null;

  const projectQuery = useQuery({
    queryKey: projectId ? projectsKeys.detail(projectId) : ['projects', 'detail', 'none'],
    enabled: projectId !== null,
    staleTime: 60_000,
    queryFn: async (): Promise<{ workspaceId: string } | null> => {
      if (!projectId) return null;
      const { data, error } = await sdk.GET('/projects/{prjId}', {
        params: { path: { prjId: projectId } },
      });
      if (error || !data) return null;
      return { workspaceId: data.workspaceId };
    },
  });

  const taskQuery = useQuery({
    queryKey: taskId
      ? (['tasks', 'detail', taskId] as const)
      : (['tasks', 'detail', 'none'] as const),
    enabled: taskId !== null,
    staleTime: 60_000,
    queryFn: async (): Promise<{ workspaceId: string } | null> => {
      if (!taskId) return null;
      const { data, error } = await sdk.GET('/tasks/{id}', {
        params: { path: { id: taskId } },
      });
      if (error || !data) return null;
      return { workspaceId: data.workspaceId };
    },
  });

  // Non-suspense read of the workspaces list. The top-bar renders the
  // suspense-variant under a Suspense boundary, so this query will
  // almost always hit the shared cache rather than issue a network
  // request. We use it to validate a persisted id against the user's
  // currently-visible workspaces.
  const workspacesQuery = useQuery({
    queryKey: workspacesKeys.list(),
    staleTime: 60_000,
    queryFn: async (): Promise<string[]> => {
      const { data, error } = await authSdk.GET('/workspaces', {});
      if (error || !data) return [];
      return (data.workspaces ?? []).map((w) => w.id);
    },
  });

  // Derive the URL-strict value first so persistence can mirror it.
  const urlWsId: string | null =
    (wsMatch ? (wsMatch[1] ?? null) : null) ??
    (projectQuery.data ? projectQuery.data.workspaceId : null) ??
    (taskQuery.data ? taskQuery.data.workspaceId : null);

  // Persist the URL-derived id whenever it changes. Kept in an effect
  // because persistence is an external-system sync; running it during
  // render would violate React's purity contract and misbehave under
  // StrictMode double-invoke.
  useEffect(() => {
    if (urlWsId) writeStoredWsId(urlWsId);
  }, [urlWsId]);

  if (urlWsId) return urlWsId;

  // Fall back to the persisted id, gated by the user's visible
  // workspaces if we have that list cached. When the list is not yet
  // loaded, return the stored id optimistically; a later render will
  // correct it if the validation fails.
  const stored = readStoredWsId();
  if (!stored) return null;
  const visible = workspacesQuery.data;
  if (visible === undefined) return stored;
  if (visible.includes(stored)) return stored;
  // Stored id is no longer reachable — purge it so we stop returning it.
  clearActiveWorkspaceId();
  return null;
}

/**
 * Alias for {@link useCurrentWorkspaceId} that emphasises the
 * "fallback-included" semantics at call sites. Use this when the caller
 * wants a workspace context regardless of whether the current URL is
 * workspace-scoped (sidebar project tree, AI dock, notifications).
 */
export function useActiveWorkspaceId(): string | null {
  return useCurrentWorkspaceId();
}
