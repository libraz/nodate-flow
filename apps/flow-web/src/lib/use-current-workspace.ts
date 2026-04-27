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

  // NOTE: we deliberately do NOT reuse `projectsKeys.detail(projectId)`
  // or `tasksKeys.detail(taskId)` here. Those keys belong to the full
  // `Project` / `Task` shapes fetched by `useProjectQuery` /
  // `useTaskQuery` in the detail pages. If this hook's stripped
  // `{ workspaceId }` projection landed at either key first (e.g. because
  // the sidebar or top-bar mounts earlier than the detail panel after a
  // client-side navigation), the detail page's `useSuspenseQuery` would
  // return the partial value straight from the cache and never refetch,
  // leaving the page rendered with empty title/priority/dates (see
  // docs/bugs/2026-04-23-web-task-detail-empty-after-spa-nav.md).
  const projectQuery = useQuery({
    queryKey: projectId
      ? (['projects', 'workspace-id', projectId] as const)
      : (['projects', 'workspace-id', 'none'] as const),
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
      ? (['tasks', 'workspace-id', taskId] as const)
      : (['tasks', 'workspace-id', 'none'] as const),
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

  // Non-suspense read of the workspaces list projected down to a bare
  // `string[]` of ids. We deliberately do NOT reuse
  // `workspacesKeys.list()` here: that key belongs to
  // `useWorkspacesQuery` which caches the full `Workspace[]` shape. If
  // this hook's ids-only projection landed at that key first (e.g.
  // because the sidebar / top-bar mounts before any caller of
  // `useWorkspacesQuery`), every downstream reader would see
  // `string[]` where `Workspace[]` is expected — leading to
  // `w.id === undefined`, `NaN` member counts, and `/workspaces/undefined`
  // links (see
  // docs/bugs/2026-04-23-web-workspace-list-cache-shape-collision.md).
  // Same pattern as the `projects` / `tasks` workspace-id projections
  // above: keep the ids-only key inline and distinct.
  const workspacesQuery = useQuery({
    queryKey: ['workspaces', 'member-ids'] as const,
    staleTime: 60_000,
    queryFn: async (): Promise<string[]> => {
      const { data, error } = await authSdk.GET('/workspaces', {});
      if (error || !data) return [];
      return (data.items ?? []).map((w) => w.id);
    },
  });

  // Derive the URL-strict value first so persistence can mirror it.
  const urlWsId: string | null =
    (wsMatch ? (wsMatch[1] ?? null) : null) ??
    (projectQuery.data ? projectQuery.data.workspaceId : null) ??
    (taskQuery.data ? taskQuery.data.workspaceId : null);

  // Resolve the fallback id in render so the return value is pure.
  // Precedence after urlWsId: a still-visible stored id wins; otherwise
  // auto-select the first visible workspace on cold start so FAB / task
  // creation flows resolve a default project without requiring the user
  // to navigate to a workspace manually first.
  const visibleList = workspacesQuery.data;
  const stored = readStoredWsId();
  let fallbackWsId: string | null = null;
  let storedInvalid = false;
  let autoSelected: string | null = null;
  if (stored) {
    if (visibleList === undefined) {
      // List not yet loaded: optimistically use the stored id. A later
      // render will correct or purge it once the list arrives.
      fallbackWsId = stored;
    } else if (visibleList.includes(stored)) {
      fallbackWsId = stored;
    } else {
      // Stored id is no longer reachable — flag for purge below.
      storedInvalid = true;
    }
  }
  if (fallbackWsId === null && visibleList && visibleList.length > 0) {
    autoSelected = visibleList[0] ?? null;
    fallbackWsId = autoSelected;
  }

  // Persist the URL-derived id whenever it changes, and mirror the
  // auto-selected fallback so subsequent renders take the stored-id
  // fast path. Kept in an effect because persistence is an
  // external-system sync; running it during render would violate
  // React's purity contract and misbehave under StrictMode
  // double-invoke.
  useEffect(() => {
    if (urlWsId) {
      writeStoredWsId(urlWsId);
      return;
    }
    if (storedInvalid) {
      clearActiveWorkspaceId();
    }
    if (autoSelected) {
      writeStoredWsId(autoSelected);
    }
  }, [urlWsId, storedInvalid, autoSelected]);

  if (urlWsId) return urlWsId;
  return fallbackWsId;
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
