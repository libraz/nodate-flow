/**
 * ActivityFeed — workspace-scoped unified activity view (audit + ai + mcp).
 *
 * Structure mirrors the timeline view (filter bar above a scrollable list +
 * a "load more" affordance) and the audit-log view (source filter + explicit
 * empty / loading / error states). Pagination is keyset-based: each page
 * carries an opaque `nextCursor`, so this uses an infinite query and a
 * "load more" button (with auto-fetch on scroll for the virtualization-free
 * list) rather than offset paging.
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { ToggleChip, ToggleChipGroup } from '@nodate-flow/ui/primitives/toggle-chip';
import { Activity as ActivityIcon } from 'lucide-react';
import { type ReactElement, Suspense, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { useWorkspaceUsersQuery } from '../workspaces/api';

import styles from './activity.module.css';
import ActivityRow from './activity-row';
import {
  ACTIVITY_SOURCES,
  type ActivityEntry,
  type ActivitySourceFilter,
  useActivityFeedQuery,
} from './api';
import { sourceLabel } from './labels';

export interface ActivityFeedProps {
  workspaceId: string;
  source: ActivitySourceFilter;
  onSourceChange: (next: ActivitySourceFilter) => void;
}

const FILTER_VALUES: readonly ActivitySourceFilter[] = ['all', ...ACTIVITY_SOURCES];

const SKELETON_ROWS = ['a', 'b', 'c', 'd', 'e'] as const;

/** Source chip filter. Single-select; `'all'` clears the stream filter. */
function SourceFilter({
  value,
  onChange,
}: {
  value: ActivitySourceFilter;
  onChange: (next: ActivitySourceFilter) => void;
}): ReactElement {
  const { t } = useTranslation('activity');
  return (
    <ToggleChipGroup label={t('filter.source_label')}>
      {FILTER_VALUES.map((v) => (
        <ToggleChip key={v} pressed={v === value} onPressedChange={() => onChange(v)}>
          {v === 'all' ? t('filter.all_sources') : sourceLabel(v, t)}
        </ToggleChip>
      ))}
    </ToggleChipGroup>
  );
}

/** Placeholder rows shaped like the feed so the list does not jump on load. */
function FeedSkeleton(): ReactElement {
  const { t } = useTranslation('activity');
  return (
    <ul className={styles.skeletonList} aria-busy="true" aria-label={t('view.loading')}>
      {SKELETON_ROWS.map((key) => (
        <li key={key} className={styles.skeletonRow}>
          <Skeleton className={styles.skeletonAvatar} />
          <Skeleton className={styles.skeletonLine} />
        </li>
      ))}
    </ul>
  );
}

/** Resolves actor display names from the workspace member list. Suspends. */
function FeedWithActors({
  workspaceId,
  source,
}: {
  workspaceId: string;
  source: ActivitySourceFilter;
}): ReactElement {
  const { t } = useTranslation('activity');
  const { data: users } = useWorkspaceUsersQuery(workspaceId);
  const nameById = new Map(users.map((u) => [u.id, u.displayName]));

  const query = useActivityFeedQuery(workspaceId, { source });
  const stagger = useFirstLoadStagger();
  const {
    data,
    error,
    isLoading,
    isFetchingNextPage,
    hasNextPage,
    fetchNextPage,
    refetch,
    isRefetching,
  } = query;

  // Auto-load the next page when a sentinel near the list end scrolls into
  // view. Falls back to the explicit "load more" button when IntersectionObserver
  // is unavailable or the user prefers it.
  const sentinelRef = useRef<HTMLLIElement | null>(null);
  // biome-ignore lint/correctness/useExhaustiveDependencies: re-arm observer when page count or fetch state changes
  useEffect(() => {
    const node = sentinelRef.current;
    if (!node || !hasNextPage || isFetchingNextPage) return;
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) {
            void fetchNextPage();
            break;
          }
        }
      },
      { rootMargin: '200px' },
    );
    io.observe(node);
    return () => io.disconnect();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage, data?.pages.length]);

  if (isLoading) {
    return <FeedSkeleton />;
  }

  if (error) {
    return (
      <div role="alert" className={styles.error}>
        <span>{formatApiError(error, t, 'view.error')}</span>
        <Button
          type="button"
          variant="default"
          onClick={() => void refetch()}
          disabled={isRefetching}
          aria-busy={isRefetching}
        >
          {t('view.retry')}
        </Button>
      </div>
    );
  }

  const entries: ActivityEntry[] = data ? data.pages.flatMap((p) => p.activity) : [];

  if (entries.length === 0) {
    return (
      <EmptyState
        className={styles.empty}
        icon={<ActivityIcon size={40} strokeWidth={1.25} aria-hidden="true" />}
        title={source === 'all' ? t('view.empty') : t('view.empty_filtered')}
        titleAs="p"
      />
    );
  }

  return (
    <>
      <ul aria-label={t('view.list_label')} className={styles.list}>
        {entries.map((entry, index) => (
          <ActivityRow
            key={entry.publicId}
            entry={entry}
            enterStyle={stagger(index)}
            actorName={entry.actorUserPublicId ? nameById.get(entry.actorUserPublicId) : undefined}
          />
        ))}
        <li ref={sentinelRef} aria-hidden="true" className={styles.sentinel} />
      </ul>

      {hasNextPage ? (
        <div className={styles.loadMore}>
          <Button
            type="button"
            variant="default"
            onClick={() => void fetchNextPage()}
            disabled={isFetchingNextPage}
            aria-busy={isFetchingNextPage}
          >
            {isFetchingNextPage ? t('view.loading_more') : t('view.load_more')}
          </Button>
        </div>
      ) : null}
    </>
  );
}

export default function ActivityFeed({
  workspaceId,
  source,
  onSourceChange,
}: ActivityFeedProps): ReactElement {
  return (
    <div className={styles.feed}>
      <SourceFilter value={source} onChange={onSourceChange} />
      <Suspense fallback={<FeedSkeleton />}>
        <FeedWithActors workspaceId={workspaceId} source={source} />
      </Suspense>
    </div>
  );
}
