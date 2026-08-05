/**
 * TimelineView — scoped (task | project | workspace) virtualised timeline
 * with filter bar and page-based "load more".
 */

import Button from '@nodate-flow/ui/primitives/button';
import { type ReactElement, useEffect, useMemo, useRef, useState } from 'react';
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
import SignalGroup from './signal-group';

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

function dayKey(ts: number): string {
  const d = new Date(ts * 1000);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
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

function TimelineInner({
  events,
  total,
  filters,
  onChange,
  workspaceId,
}: InnerProps): ReactElement {
  const { t, i18n } = useTranslation('timeline');
  const locale = i18n.resolvedLanguage ?? 'en';

  // Accumulate events across pages. When filters change (kind, actor)
  // we reset; when only offset advances we append the new page.
  const [accumulated, setAccumulated] = useState<TimelineEvent[]>([]);
  const prevFiltersRef = useRef(filters);

  useEffect(() => {
    const prev = prevFiltersRef.current;
    const filtersChanged =
      prev.kind !== filters.kind || prev.actor !== filters.actor || prev.limit !== filters.limit;
    if (filtersChanged || (filters.offset ?? 0) === 0) {
      setAccumulated(events);
    } else {
      setAccumulated((acc) => {
        const existingIds = new Set(acc.map((e) => e.id));
        const newEvents = events.filter((e) => !existingIds.has(e.id));
        return [...acc, ...newEvents];
      });
    }
    prevFiltersRef.current = filters;
  }, [events, filters]);

  const allEvents = accumulated;

  /** Group events by local-time day, preserving server order. */
  const groups = useMemo<{ key: string; label: string; items: TimelineEvent[] }[]>(() => {
    const fmt = new Intl.DateTimeFormat(locale, {
      year: 'numeric',
      month: 'long',
      day: 'numeric',
      weekday: 'short',
    });
    const todayKey = dayKey(Math.floor(Date.now() / 1000));
    const yesterdayKey = dayKey(Math.floor(Date.now() / 1000) - 86_400);
    const out: { key: string; label: string; items: TimelineEvent[] }[] = [];
    for (const ev of allEvents) {
      const key = dayKey(ev.occurredAt);
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
  }, [allEvents, locale, t]);

  const hasMore = allEvents.length < total;

  const handleLoadMore = (): void => {
    onChange({ ...filters, offset: allEvents.length });
  };

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-3)',
        minBlockSize: 0,
      }}
    >
      <EventFilterBar
        filters={filters}
        onChange={onChange}
        {...(workspaceId !== undefined ? { workspaceId } : {})}
      />

      {allEvents.length === 0 ? (
        <div
          style={{
            padding: 'var(--nf-space-12) var(--nf-space-4)',
            textAlign: 'center',
            color: 'var(--nf-color-fg-muted)',
            border: '1px dashed var(--nf-color-border)',
            borderRadius: 'var(--nf-radius-lg)',
            background: 'var(--nf-color-bg-sunken)',
          }}
        >
          {t('view.empty')}
        </div>
      ) : (
        <div
          style={{
            maxBlockSize: '40rem',
            overflowY: 'auto',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-5)',
            paddingInlineEnd: 'var(--nf-space-2)',
          }}
        >
          {groups.map((g) => (
            <section key={g.key} aria-label={g.label}>
              <h3
                style={{
                  position: 'sticky',
                  insetBlockStart: 0,
                  zIndex: 2,
                  margin: 0,
                  padding: '0.375rem var(--nf-space-3)',
                  fontSize: 'var(--nf-text-xs)',
                  textTransform: 'uppercase',
                  letterSpacing: '0.05em',
                  color: 'var(--nf-color-fg-muted)',
                  background: 'var(--nf-color-bg)',
                  borderBlockEnd: '1px solid var(--nf-color-border)',
                }}
              >
                {g.label} · {g.items.length}
              </h3>
              <div
                style={{
                  position: 'relative',
                  paddingInlineStart: 'var(--nf-space-2)',
                  paddingBlockStart: 'var(--nf-space-1)',
                }}
              >
                {/* vertical rail */}
                <div
                  aria-hidden
                  style={{
                    position: 'absolute',
                    insetInlineStart: 'calc(var(--nf-space-2) + 0.875rem - 1px)',
                    insetBlockStart: 0,
                    insetBlockEnd: 0,
                    inlineSize: '2px',
                    background: 'var(--nf-color-border)',
                  }}
                />
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
