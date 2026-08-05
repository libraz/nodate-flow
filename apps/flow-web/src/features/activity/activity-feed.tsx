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

import Button from '@nodate-flow/ui/primitives/button';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { type ReactElement, Suspense, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { useWorkspaceUsersQuery } from '../workspaces/api';

import ActivityRow from './activity-row';
import {
  ACTIVITY_SOURCES,
  type ActivityEntry,
  type ActivitySourceFilter,
  useActivityFeedQuery,
} from './api';

export interface ActivityFeedProps {
  workspaceId: string;
  source: ActivitySourceFilter;
  onSourceChange: (next: ActivitySourceFilter) => void;
}

const FILTER_VALUES: readonly ActivitySourceFilter[] = ['all', ...ACTIVITY_SOURCES];

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
    <div
      role="group"
      aria-label={t('filter.source_label')}
      style={{ display: 'flex', flexWrap: 'wrap', gap: '0.375rem' }}
    >
      {FILTER_VALUES.map((v) => {
        const active = v === value;
        const label = v === 'all' ? t('filter.all_sources') : t(`source.${v}`);
        return (
          <button
            key={v}
            type="button"
            aria-pressed={active}
            onClick={() => onChange(v)}
            style={{
              padding: 'var(--nf-space-1) var(--nf-space-3)',
              borderRadius: 'var(--nf-radius-pill)',
              border: `1px solid ${active ? 'var(--nf-color-accent)' : 'var(--nf-color-border)'}`,
              background: active ? 'var(--nf-color-accent-subtle)' : 'var(--nf-color-surface)',
              color: active ? 'var(--nf-color-accent)' : 'var(--nf-color-fg)',
              fontSize: '0.8125rem',
              cursor: 'pointer',
              transition:
                'border-color var(--nf-duration-fast) ease, background-color var(--nf-duration-fast) ease',
            }}
          >
            {label}
          </button>
        );
      })}
    </div>
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
  const sentinelRef = useRef<HTMLDivElement | null>(null);
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
    return (
      <div style={{ padding: 'var(--nf-space-8)', display: 'flex', justifyContent: 'center' }}>
        <Spinner label={t('view.loading')} />
      </div>
    );
  }

  if (error) {
    return (
      <div
        role="alert"
        style={{
          padding: 'var(--nf-space-8) var(--nf-space-4)',
          textAlign: 'center',
          color: 'var(--nf-color-danger)',
          border: '1px solid var(--nf-color-danger)',
          borderRadius: 'var(--nf-radius-lg)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-3)',
          alignItems: 'center',
        }}
      >
        <span>{formatApiError(error, t, 'view.error')}</span>
        <Button
          type="button"
          variant="default"
          onClick={() => void refetch()}
          disabled={isRefetching}
        >
          {t('view.retry')}
        </Button>
      </div>
    );
  }

  const entries: ActivityEntry[] = data ? data.pages.flatMap((p) => p.activity) : [];

  if (entries.length === 0) {
    return (
      <div
        style={{
          padding: 'var(--nf-space-12) var(--nf-space-4)',
          textAlign: 'center',
          color: 'var(--nf-color-fg-muted)',
          border: '1px dashed var(--nf-color-border)',
          borderRadius: 'var(--nf-radius-lg)',
          background: 'var(--nf-color-bg-sunken)',
          fontSize: 'var(--nf-text-sm)',
        }}
      >
        {source === 'all' ? t('view.empty') : t('view.empty_filtered')}
      </div>
    );
  }

  return (
    <>
      <ul
        aria-label={t('view.list_label')}
        style={{
          listStyle: 'none',
          margin: 0,
          padding: 0,
          // nf-token-override: component dimension, not a spacing step
          maxBlockSize: '40rem',
          overflowY: 'auto',
          border: '1px solid var(--nf-color-border)',
          borderRadius: 'var(--nf-radius-lg)',
          background: 'var(--nf-color-surface)',
        }}
      >
        {entries.map((entry) => (
          <ActivityRow
            key={entry.publicId}
            entry={entry}
            actorName={entry.actorUserPublicId ? nameById.get(entry.actorUserPublicId) : undefined}
          />
        ))}
        <div ref={sentinelRef} aria-hidden="true" style={{ blockSize: '1px' }} />
      </ul>

      {hasNextPage ? (
        <div style={{ display: 'flex', justifyContent: 'center' }}>
          <Button
            type="button"
            variant="default"
            onClick={() => void fetchNextPage()}
            disabled={isFetchingNextPage}
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
  const { t } = useTranslation('activity');
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-3)',
        minBlockSize: 0,
      }}
    >
      <SourceFilter value={source} onChange={onSourceChange} />
      <Suspense
        fallback={
          <div style={{ padding: 'var(--nf-space-8)', display: 'flex', justifyContent: 'center' }}>
            <Spinner label={t('view.loading')} />
          </div>
        }
      >
        <FeedWithActors workspaceId={workspaceId} source={source} />
      </Suspense>
    </div>
  );
}
