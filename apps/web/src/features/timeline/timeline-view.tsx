/**
 * TimelineView — scoped (task | project | workspace) virtualised timeline
 * with filter bar and page-based "load more".
 */

import Button from '@nodate-flow/ui/primitives/button';
import { useVirtualizer } from '@tanstack/react-virtual';
import { type ReactElement, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  type TimelineEvent,
  type TimelineFilters,
  useProjectTimelineQuery,
  useTaskTimelineQuery,
  useWorkspaceTimelineQuery,
} from './api';
import EventCard from './event-card';
import EventFilterBar from './event-filter-bar';

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
  total: number;
  filters: TimelineFilters;
  onChange: (next: TimelineFilters) => void;
  workspaceId?: string;
}

function TimelineInner({
  events,
  total,
  filters,
  onChange,
  workspaceId,
}: InnerProps): ReactElement {
  const { t } = useTranslation('timeline');
  const scrollRef = useRef<HTMLDivElement>(null);

  const virtualizer = useVirtualizer({
    count: events.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 96,
    overscan: 5,
    measureElement: (el) => el.getBoundingClientRect().height,
  });

  const virtualRows = virtualizer.getVirtualItems();
  // happy-dom has no layout; fall back to rendering all rows for tests.
  const useFallback = virtualRows.length === 0 && events.length > 0;
  const totalSize = useFallback ? events.length * 80 : virtualizer.getTotalSize();

  const limit = filters.limit ?? 50;
  const offset = filters.offset ?? 0;
  const hasMore = offset + events.length < total;

  const handleLoadMore = (): void => {
    // TODO: proper infinite scroll accumulator. For now we just page forward
    // (replaces the current page rather than appending).
    onChange({ ...filters, offset: offset + limit });
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem', minBlockSize: 0 }}>
      <EventFilterBar
        filters={filters}
        onChange={onChange}
        {...(workspaceId !== undefined ? { workspaceId } : {})}
      />

      {events.length === 0 ? (
        <div style={{ padding: '2rem', textAlign: 'center', color: 'var(--color-muted)' }}>
          {t('view.empty')}
        </div>
      ) : (
        <div
          ref={scrollRef}
          style={{
            overflowY: 'auto',
            maxBlockSize: '32rem',
            position: 'relative',
          }}
        >
          <div style={{ blockSize: totalSize, position: 'relative' }}>
            {useFallback
              ? events.map((ev) => (
                  <div key={ev.id} style={{ padding: '0.25rem 0' }}>
                    <EventCard event={ev} />
                  </div>
                ))
              : virtualRows.map((vr) => {
                  const ev = events[vr.index];
                  if (!ev) return null;
                  return (
                    <div
                      key={ev.id}
                      data-index={vr.index}
                      ref={virtualizer.measureElement}
                      style={{
                        position: 'absolute',
                        insetBlockStart: 0,
                        insetInlineStart: 0,
                        inlineSize: '100%',
                        transform: `translateY(${vr.start}px)`,
                        padding: '0.25rem 0',
                      }}
                    >
                      <EventCard event={ev} />
                    </div>
                  );
                })}
          </div>
        </div>
      )}

      {hasMore ? (
        <div style={{ display: 'flex', justifyContent: 'center' }}>
          <Button type="button" variant="default" onClick={handleLoadMore}>
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
  workspaceId?: string;
}

function TaskTimeline({ id, filters, onChange, workspaceId }: ScopedProps): ReactElement {
  const { data } = useTaskTimelineQuery(id, filters);
  return (
    <TimelineInner
      events={data.events}
      total={data.total}
      filters={filters}
      onChange={onChange}
      {...(workspaceId !== undefined ? { workspaceId } : {})}
    />
  );
}

function ProjectTimeline({ id, filters, onChange, workspaceId }: ScopedProps): ReactElement {
  const { data } = useProjectTimelineQuery(id, filters);
  return (
    <TimelineInner
      events={data.events}
      total={data.total}
      filters={filters}
      onChange={onChange}
      {...(workspaceId !== undefined ? { workspaceId } : {})}
    />
  );
}

function WorkspaceTimeline({ id, filters, onChange }: ScopedProps): ReactElement {
  const { data } = useWorkspaceTimelineQuery(id, filters);
  return (
    <TimelineInner
      events={data.events}
      total={data.total}
      filters={filters}
      onChange={onChange}
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

  if (scope.kind === 'task') {
    return (
      <TaskTimeline
        id={scope.id}
        filters={filters}
        onChange={setFilters}
        {...(workspaceId !== undefined ? { workspaceId } : {})}
      />
    );
  }
  if (scope.kind === 'project') {
    return (
      <ProjectTimeline
        id={scope.id}
        filters={filters}
        onChange={setFilters}
        {...(workspaceId !== undefined ? { workspaceId } : {})}
      />
    );
  }
  return <WorkspaceTimeline id={scope.id} filters={filters} onChange={setFilters} />;
}
