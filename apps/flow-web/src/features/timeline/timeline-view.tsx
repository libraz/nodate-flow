/**
 * TimelineView — scoped (task | project | workspace) virtualised timeline
 * with filter bar and page-based "load more".
 */

import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import type { Zone } from '@nodate-flow/ui/time';
import { type ReactElement, useMemo, useState, useTransition } from 'react';
import { useTranslation } from 'react-i18next';
import { eventDateKey } from '../../lib/date-utils';
import { useEffectiveZone } from '../../lib/use-effective-timezone';
import {
  type TimelineEvent,
  type TimelineFilters,
  type TimelinePage,
  useProjectTimelineInfiniteQuery,
  useTaskTimelineInfiniteQuery,
  useWorkspaceTimelineInfiniteQuery,
} from './api';
import EventCard from './event-card';
import EventFilterBar, { resetFilters } from './event-filter-bar';
import SignalGroup from './signal-group';
import styles from './timeline-view.module.css';

export type TimelineScopeArg =
  | { kind: 'task'; id: string }
  | { kind: 'project'; id: string }
  | { kind: 'workspace'; id: string };

export interface TimelineViewProps {
  scope: TimelineScopeArg;
  initialFilters?: TimelineFilters;
  /**
   * Workspace id used to populate the actor filter picker. For workspace
   * scope this is the scope id; for project / task scope it must be
   * threaded from the parent route when available.
   */
  workspaceId?: string;
}

interface InnerProps {
  events: TimelineEvent[];
  filters: TimelineFilters;
  onChange: (next: TimelineFilters) => void;
  /** True while a filter is loading behind the current results. */
  pending: boolean;
  hasMore: boolean;
  fetchingMore: boolean;
  onLoadMore: () => void;
  workspaceId?: string;
}

/**
 * The calendar day an event's instant falls on, read in `zone`.
 *
 * Both the group boundaries and the "Today" / "Yesterday" labels come
 * from this, so reading it in the browser's zone put an event under a
 * heading a day away from the one every other surface files it under.
 */
function dayKey(ts: number, zone: Zone): string {
  return eventDateKey(ts, false, zone);
}

/**
 * SignalCluster — adjacent events that share a `triggeredBySignalId`.
 * Events without that field surface as singleton "solo" clusters so the
 * caller can render them with the legacy `EventCard` layout.
 *
 * Adjacency rather than full grouping is intentional: the server orders
 * the timeline by `occurredAt DESC` and signal-driven events from one
 * judge run are emitted in the same DB transaction, so they arrive
 * contiguously. Keeping the cluster local to its slice preserves the
 * temporal interleaving when the same signal fires multiple times.
 */
type SignalCluster =
  | { kind: 'solo'; events: TimelineEvent[] }
  | { kind: 'signal'; signalId: string; events: TimelineEvent[] };

function clusterBySignal(events: TimelineEvent[]): SignalCluster[] {
  const out: SignalCluster[] = [];
  for (const ev of events) {
    const sig =
      ev.triggeredBySignalId !== undefined && ev.triggeredBySignalId.length > 0
        ? ev.triggeredBySignalId
        : null;
    const last = out[out.length - 1];
    if (sig !== null) {
      if (last && last.kind === 'signal' && last.signalId === sig) {
        last.events.push(ev);
      } else {
        out.push({ kind: 'signal', signalId: sig, events: [ev] });
      }
    } else if (last && last.kind === 'solo') {
      last.events.push(ev);
    } else {
      out.push({ kind: 'solo', events: [ev] });
    }
  }
  return out;
}

/** Flatten loaded pages in server order, replacing duplicate IDs in place. */
function mergeTimelinePages(pages: readonly TimelinePage[]): TimelineEvent[] {
  const merged: TimelineEvent[] = [];
  const positions = new Map<string, number>();
  for (const page of pages) {
    for (const event of page.events) {
      const position = positions.get(event.id);
      if (position === undefined) {
        positions.set(event.id, merged.length);
        merged.push(event);
      } else {
        merged[position] = event;
      }
    }
  }
  return merged;
}

function TimelineInner({
  events,
  filters,
  onChange,
  pending,
  hasMore,
  fetchingMore,
  onLoadMore,
  workspaceId,
}: InnerProps): ReactElement {
  const { t, i18n } = useTranslation('timeline');
  const locale = i18n.resolvedLanguage ?? 'en';
  const zone = useEffectiveZone();

  const allEvents = events;

  /** Group events by local-time day, preserving server order. */
  const groups = useMemo<{ key: string; label: string; items: TimelineEvent[] }[]>(() => {
    const fmt = new Intl.DateTimeFormat(locale, {
      year: 'numeric',
      month: 'long',
      day: 'numeric',
      weekday: 'short',
    });
    const nowSeconds = Math.floor(Date.now() / 1000);
    const todayKey = dayKey(nowSeconds, zone);
    const yesterdayKey = dayKey(nowSeconds - 86_400, zone);
    const out: { key: string; label: string; items: TimelineEvent[] }[] = [];
    for (const ev of allEvents) {
      const key = dayKey(ev.occurredAt, zone);
      let group = out[out.length - 1];
      if (!group || group.key !== key) {
        let label: string;
        if (key === todayKey) label = t('view.today', { defaultValue: 'Today' });
        else if (key === yesterdayKey) label = t('view.yesterday', { defaultValue: 'Yesterday' });
        else label = fmt.format(new Date(ev.occurredAt * 1000));
        group = { key, label, items: [] };
        out.push(group);
      }
      group.items.push(ev);
    }
    return out;
  }, [allEvents, locale, t, zone]);

  const filtersActive = (filters.kind?.length ?? 0) > 0 || (filters.actor?.length ?? 0) > 0;

  return (
    <div aria-busy={pending || fetchingMore || undefined} className={styles.root}>
      <EventFilterBar
        filters={filters}
        onChange={onChange}
        {...(workspaceId !== undefined ? { workspaceId } : {})}
      />

      {allEvents.length === 0 ? (
        filtersActive ? (
          <EmptyState
            titleAs="p"
            title={t('view.empty_filtered')}
            action={
              <Button
                type="button"
                variant="default"
                onClick={() => onChange(resetFilters(filters))}
              >
                {t('filter.reset')}
              </Button>
            }
          />
        ) : (
          <EmptyState titleAs="p" title={t('view.empty')} />
        )
      ) : (
        <div className={styles.scroller} data-busy={pending || fetchingMore ? 'true' : 'false'}>
          {groups.map((g) => (
            <section key={g.key} aria-label={g.label}>
              <h3 className={styles.dayHeading}>
                {g.label} · <span className={styles.dayCount}>{g.items.length}</span>
              </h3>
              <div className={styles.rows}>
                {/* vertical rail */}
                <div aria-hidden className={styles.rail} />
                {clusterBySignal(g.items).map((cluster) =>
                  cluster.kind === 'signal' && workspaceId !== undefined ? (
                    <SignalGroup
                      key={`sig-${cluster.signalId}`}
                      signalId={cluster.signalId}
                      workspaceId={workspaceId}
                      events={cluster.events}
                    />
                  ) : (
                    // Fall back to a flat list when we have no workspace
                    // context (task-scoped embed) — the reverse action
                    // is unavailable there anyway.
                    cluster.events.map((ev) => <EventCard key={ev.id} event={ev} />)
                  ),
                )}
              </div>
            </section>
          ))}
        </div>
      )}

      {hasMore ? (
        <div className={styles.loadMore}>
          <Button
            type="button"
            variant="default"
            onClick={onLoadMore}
            aria-busy={pending || fetchingMore || undefined}
            disabled={pending || fetchingMore}
          >
            {t('view.load_more')}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

interface ScopedProps {
  id: string;
  filters: TimelineFilters;
  onChange: (next: TimelineFilters) => void;
  pending: boolean;
  workspaceId?: string;
}

function TaskTimeline({ id, filters, onChange, pending, workspaceId }: ScopedProps): ReactElement {
  const query = useTaskTimelineInfiniteQuery(id, filters);
  const events = mergeTimelinePages(query.data.pages);
  return (
    <TimelineInner
      events={events}
      filters={filters}
      onChange={onChange}
      pending={pending}
      hasMore={Boolean(query.hasNextPage)}
      fetchingMore={query.isFetchingNextPage}
      onLoadMore={() => void query.fetchNextPage()}
      {...(workspaceId !== undefined ? { workspaceId } : {})}
    />
  );
}

function ProjectTimeline({
  id,
  filters,
  onChange,
  pending,
  workspaceId,
}: ScopedProps): ReactElement {
  const query = useProjectTimelineInfiniteQuery(id, filters);
  const events = mergeTimelinePages(query.data.pages);
  return (
    <TimelineInner
      events={events}
      filters={filters}
      onChange={onChange}
      pending={pending}
      hasMore={Boolean(query.hasNextPage)}
      fetchingMore={query.isFetchingNextPage}
      onLoadMore={() => void query.fetchNextPage()}
      {...(workspaceId !== undefined ? { workspaceId } : {})}
    />
  );
}

function WorkspaceTimeline({ id, filters, onChange, pending }: ScopedProps): ReactElement {
  const query = useWorkspaceTimelineInfiniteQuery(id, filters);
  const events = mergeTimelinePages(query.data.pages);
  return (
    <TimelineInner
      events={events}
      filters={filters}
      onChange={onChange}
      pending={pending}
      hasMore={Boolean(query.hasNextPage)}
      fetchingMore={query.isFetchingNextPage}
      onLoadMore={() => void query.fetchNextPage()}
      workspaceId={id}
    />
  );
}

export default function TimelineView({
  scope,
  initialFilters,
  workspaceId,
}: TimelineViewProps): ReactElement {
  const [filters, setFilters] = useState<TimelineFilters>(
    initialFilters ?? { limit: 50, offset: 0 },
  );
  // A transition keeps the current events on screen while a new filter
  // query suspends, instead of dropping back to the route's fallback.
  const [pending, startTransition] = useTransition();
  const handleChange = (next: TimelineFilters): void => {
    const filtersChanged =
      filters.kind !== next.kind || filters.actor !== next.actor || filters.limit !== next.limit;
    startTransition(() => {
      setFilters(filtersChanged ? { ...next, offset: 0 } : next);
    });
  };

  if (scope.kind === 'task') {
    return (
      <TaskTimeline
        id={scope.id}
        filters={filters}
        onChange={handleChange}
        pending={pending}
        {...(workspaceId !== undefined ? { workspaceId } : {})}
      />
    );
  }
  if (scope.kind === 'project') {
    return (
      <ProjectTimeline
        id={scope.id}
        filters={filters}
        onChange={handleChange}
        pending={pending}
        {...(workspaceId !== undefined ? { workspaceId } : {})}
      />
    );
  }
  return (
    <WorkspaceTimeline id={scope.id} filters={filters} onChange={handleChange} pending={pending} />
  );
}
