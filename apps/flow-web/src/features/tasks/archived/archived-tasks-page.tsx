/**
 * ArchivedTasksPage — `/workspaces/{wsId}/tasks/archived`.
 *
 * The page orchestrates the Archive Room surface end to end:
 *
 *   - Fetches the archived task list for the workspace via TanStack
 *     Query (`useArchivedTasksQuery`, suspense).
 *   - Loads the workspace's projects + members so the filter bar can
 *     render named picker options instead of opaque IDs.
 *   - Owns the filter state (`useArchiveFilters`) and applies it
 *     client-side: substring search, time-range pill, project pin,
 *     archiver pin.
 *   - Groups the filtered rows into chapter strata via
 *     {@link useTimeStrata}.
 *   - Owns the bulk selection set (`useBulkSelection`) and dispatches
 *     bulk / single unarchive mutations.
 *   - Branches on the rendering mode: skeleton while data loads,
 *     error fallback on query failure, two distinct empty states
 *     (truly empty vs filtered-to-zero), and the virtualized list
 *     otherwise.
 *
 * Bulk unarchive runs through `Promise.allSettled` so a single 4xx
 * does not blow up the whole batch; success and per-failure toasts
 * are emitted in parallel and the row's optimistic strike-through
 * is rolled back if the mutation rejected.
 *
 * The page uses Suspense + ErrorBoundary at the route level — see
 * the route file — so this component does not have to gate its own
 * loading. The skeleton you see here is therefore the *route's*
 * Suspense boundary doing the work; we still keep `ArchivedSkeleton`
 * around for the zero-rows-for-this-filter intermediate.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useProjectsQuery } from '../../projects/api';
import { useWorkspaceMembersQuery } from '../../workspaces/api';
import { type TaskListItem, useUnarchiveTask } from '../api';

import ArchivedBulkActionBar from './archived-bulk-action-bar';
import ArchivedEmptyFiltered from './archived-empty-filtered';
import ArchivedEmptyState from './archived-empty-state';
import ArchivedFilters from './archived-filters';
import ArchivedHeader from './archived-header';
import ArchivedList from './archived-list';
import styles from './archived.module.css';
import { type ArchiveRange, useArchiveFilters } from './hooks/use-archive-filters';
import { useArchivedTasksQuery } from './hooks/use-archived-tasks';
import { useBulkSelection } from './hooks/use-bulk-selection';
import { useTimeStrata } from './hooks/use-time-strata';

/** Range pill → relative seconds threshold (epoch >= now - range). */
const RANGE_SECONDS: Record<Exclude<ArchiveRange, 'all'>, number> = {
  '7d': 60 * 60 * 24 * 7,
  '30d': 60 * 60 * 24 * 30,
  '90d': 60 * 60 * 24 * 90,
};

interface ArchivedTasksPageProps {
  workspaceId: string;
}

/**
 * Filter the raw archived list down to the user's current pills.
 * Pure function so we can reason about it independently from React.
 */
function applyFilters(
  rows: readonly TaskListItem[],
  search: string,
  range: ArchiveRange,
  projectId: string,
  archiverId: string,
  nowSeconds: number,
): TaskListItem[] {
  const trimmed = search.trim().toLowerCase();
  const rangeFloor = range === 'all' ? null : nowSeconds - RANGE_SECONDS[range];
  const out: TaskListItem[] = [];
  for (const row of rows) {
    if (trimmed.length > 0 && !row.title.toLowerCase().includes(trimmed)) continue;
    if (projectId.length > 0 && row.projectId !== projectId) continue;
    if (archiverId.length > 0 && row.primaryAssigneeId !== archiverId) continue;
    if (rangeFloor !== null) {
      if (row.archivedAt === undefined) continue;
      if (row.archivedAt < rangeFloor) continue;
    }
    out.push(row);
  }
  return out;
}

export default function ArchivedTasksPage({ workspaceId }: ArchivedTasksPageProps): ReactElement {
  const { t, i18n } = useTranslation('archive');
  const locale = i18n.resolvedLanguage ?? 'en';

  const archivedQuery = useArchivedTasksQuery(workspaceId);
  const projectsQuery = useProjectsQuery(workspaceId);
  const membersQuery = useWorkspaceMembersQuery(workspaceId);
  const unarchive = useUnarchiveTask();

  const filtersApi = useArchiveFilters();
  const { filters, setSearch, setRange, setProjectId, setArchiverId, reset, isFiltered } =
    filtersApi;

  const allRows = archivedQuery.tasks;
  const total = archivedQuery.total;
  const { hasNextPage, fetchNextPage, isFetchingNextPage } = archivedQuery;

  const filteredRows = useMemo(() => {
    const nowSeconds = Math.floor(Date.now() / 1000);
    return applyFilters(
      allRows,
      filters.search,
      filters.range,
      filters.projectId,
      filters.archiverId,
      nowSeconds,
    );
  }, [allRows, filters.search, filters.range, filters.projectId, filters.archiverId]);

  const groups = useTimeStrata(filteredRows);

  const orderedIds = useMemo(() => {
    const ids: string[] = [];
    for (const g of groups) {
      for (const r of g.rows) ids.push(r.id);
    }
    return ids;
  }, [groups]);

  const bulk = useBulkSelection({ orderedIds });
  const [removing, setRemoving] = useState<ReadonlySet<string>>(() => new Set());

  // Member lookup tables for archiver chips. Today the API does not
  // return the archiver, so we fall back to the primary assignee — see
  // the hook's docstring.
  const archiverNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const m of membersQuery.data) map.set(m.userId, m.displayName);
    return map;
  }, [membersQuery.data]);
  const archiverAvatarById = useMemo(() => {
    const map = new Map<string, string>();
    for (const m of membersQuery.data) {
      if (m.avatarUrl) map.set(m.userId, m.avatarUrl);
    }
    return map;
  }, [membersQuery.data]);

  const lastArchivedAt = useMemo<number | undefined>(() => {
    let max: number | undefined;
    for (const r of filteredRows) {
      if (r.archivedAt === undefined) continue;
      if (max === undefined || r.archivedAt > max) max = r.archivedAt;
    }
    return max;
  }, [filteredRows]);

  const titleById = useMemo(() => {
    const map = new Map<string, string>();
    for (const r of allRows) map.set(r.id, r.title);
    return map;
  }, [allRows]);

  const markRemoving = useCallback((ids: readonly string[], on: boolean): void => {
    setRemoving((prev) => {
      const next = new Set(prev);
      for (const id of ids) {
        if (on) next.add(id);
        else next.delete(id);
      }
      return next;
    });
  }, []);

  const handleUnarchiveOne = useCallback(
    async (id: string): Promise<void> => {
      markRemoving([id], true);
      try {
        await unarchive.mutateAsync(id);
        const title = titleById.get(id) ?? '';
        toaster.show({
          tone: 'success',
          message: t('toast.unarchivedOne', { title }),
        });
      } catch {
        markRemoving([id], false);
        const title = titleById.get(id) ?? '';
        toaster.show({
          tone: 'danger',
          message: t('error.unarchiveFailed', { title }),
        });
      }
    },
    [markRemoving, t, titleById, unarchive],
  );

  const handleUnarchiveSelected = useCallback(async (): Promise<void> => {
    const ids = [...bulk.selected];
    if (ids.length === 0) return;
    markRemoving(ids, true);
    const results = await Promise.allSettled(ids.map((id) => unarchive.mutateAsync(id)));
    const failedIds: string[] = [];
    results.forEach((res, idx) => {
      if (res.status === 'rejected') {
        const id = ids[idx];
        if (id) failedIds.push(id);
      }
    });
    if (failedIds.length > 0) {
      markRemoving(failedIds, false);
      for (const id of failedIds) {
        const title = titleById.get(id) ?? '';
        toaster.show({
          tone: 'danger',
          message: t('error.unarchiveFailed', { title }),
        });
      }
    }
    const successCount = ids.length - failedIds.length;
    if (successCount > 0) {
      toaster.show({
        tone: 'success',
        message: t('toast.unarchivedMany', { count: successCount }),
      });
    }
    bulk.clear();
  }, [bulk, markRemoving, t, titleById, unarchive]);

  const handleActivate = useCallback((_id: string): void => {
    // The row title is itself a `<Link>` to `/tasks/$taskId`, so the
    // page does not need to navigate imperatively. The handler exists
    // so the row can call it from Enter without side effects.
  }, []);

  const handleClearFilters = useCallback((): void => {
    reset();
  }, [reset]);

  // Render branches. Hard errors throw to the route-level ErrorBoundary
  // because the underlying query is suspense-driven; we only need to
  // distinguish empty/filtered/list here.
  // 1. Truly empty (no archive at all) → "Nothing stored yet" CTA.
  // 2. Filtered to zero → "Couldn't find any matching archive".
  // 3. Otherwise → virtualized list with infinite-scroll prefetch.
  let body: ReactElement;
  if (total === 0) {
    body = <ArchivedEmptyState workspaceId={workspaceId} />;
  } else if (filteredRows.length === 0) {
    body = <ArchivedEmptyFiltered onClearFilters={handleClearFilters} />;
  } else {
    body = (
      <ArchivedList
        groups={groups}
        orderedIds={orderedIds}
        selected={bulk.selected}
        removing={removing}
        archiverNameById={archiverNameById}
        archiverAvatarById={archiverAvatarById}
        locale={locale}
        hasNextPage={hasNextPage}
        isFetchingNextPage={isFetchingNextPage}
        onLoadMore={() => {
          void fetchNextPage();
        }}
        onToggleSelect={bulk.toggle}
        onUnarchive={(id) => {
          void handleUnarchiveOne(id);
        }}
        onUnarchiveSelected={() => {
          void handleUnarchiveSelected();
        }}
        onActivate={handleActivate}
      />
    );
  }

  return (
    <main className={styles.page} aria-labelledby="archive-title">
      <ArchivedHeader count={filteredRows.length} lastArchivedAt={lastArchivedAt} locale={locale} />

      <ArchivedFilters
        search={filters.search}
        range={filters.range}
        projectId={filters.projectId}
        archiverId={filters.archiverId}
        projects={projectsQuery.data}
        members={membersQuery.data}
        onSearchChange={setSearch}
        onRangeChange={setRange}
        onProjectChange={setProjectId}
        onArchiverChange={setArchiverId}
      />

      {isFiltered && total > 0 && filteredRows.length > 0 ? (
        <div>
          <Button type="button" variant="ghost" size="sm" onClick={handleClearFilters}>
            {t('empty.filteredCta')}
          </Button>
        </div>
      ) : null}

      {body}

      <ArchivedBulkActionBar
        count={bulk.count}
        busy={unarchive.isPending}
        onUnarchive={() => {
          void handleUnarchiveSelected();
        }}
        onCancel={bulk.clear}
      />
    </main>
  );
}
