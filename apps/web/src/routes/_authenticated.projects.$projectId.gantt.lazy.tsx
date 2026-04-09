/**
 * /projects/$projectId/gantt — lightweight self-rendered Gantt view.
 *
 * Read-only observation view per docs/requirements.md §1.5: no drag,
 * resize, critical-path, or zoom. Bars are drawn as plain SVG between
 * `started_on` and `due_on`. Tasks missing both dates are listed as
 * "unscheduled" and excluded from the chart. Click a bar or row label
 * to drill into the task detail panel.
 */

import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { useSuspenseQuery } from '@tanstack/react-query';
import { createLazyFileRoute, getRouteApi, useNavigate } from '@tanstack/react-router';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { type ReactElement, Suspense, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useProjectDependenciesQuery } from '../features/projects/api';
import { sdk } from '../lib/sdk';

type TaskListItem = components['schemas']['TaskListItem'];

const routeApi = getRouteApi('/_authenticated/projects/$projectId/gantt');

const STATE_COLOR: Record<string, string> = {
  open: 'var(--color-info, #3498db)',
  waiting: 'var(--color-warning, #f39c12)',
  review: 'var(--color-accent, #9b59b6)',
  done: 'var(--color-success, #27ae60)',
  cancelled: 'var(--color-muted, #95a5a6)',
};

const ROW_HEIGHT = 28;
const ROW_GAP = 4;
const HEADER_HEIGHT = 36;
const DAY_WIDTH = 24;
const LABEL_WIDTH = 220;

function startOfDay(d: Date): Date {
  const x = new Date(d);
  x.setHours(0, 0, 0, 0);
  return x;
}

function parseDateOnly(s: string): Date | null {
  // YYYY-MM-DD → local-time midnight
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  if (!m) return null;
  const y = Number.parseInt(m[1] ?? '0', 10);
  const mo = Number.parseInt(m[2] ?? '1', 10) - 1;
  const da = Number.parseInt(m[3] ?? '1', 10);
  return new Date(y, mo, da);
}

function diffDays(a: Date, b: Date): number {
  const ms = startOfDay(b).getTime() - startOfDay(a).getTime();
  return Math.round(ms / 86400000);
}

function addDays(d: Date, n: number): Date {
  const x = new Date(d);
  x.setDate(x.getDate() + n);
  return x;
}

interface ScheduledTask {
  task: TaskListItem;
  start: Date;
  end: Date;
}

function GanttRoute(): ReactElement {
  return (
    <Suspense fallback={<Skeleton style={{ blockSize: '20rem', inlineSize: '100%' }} />}>
      <GanttView />
    </Suspense>
  );
}

function GanttView(): ReactElement {
  const { t } = useTranslation('common');
  const { projectId } = routeApi.useParams();
  const navigate = useNavigate();
  const [offsetDays, setOffsetDays] = useState(0);

  const { data: tasks } = useSuspenseQuery({
    queryKey: ['tasks', 'list', projectId, 'gantt'] as const,
    staleTime: 30_000,
    queryFn: async (): Promise<TaskListItem[]> => {
      const { data, error } = await sdk.GET('/tasks', {
        params: { query: { projectId, limit: 200, offset: 0 } },
      });
      if (error || !data) return [];
      return data.tasks ?? [];
    },
  });

  const { data: edges } = useProjectDependenciesQuery(projectId);

  const { scheduled, unscheduled } = useMemo(() => {
    const sc: ScheduledTask[] = [];
    let un = 0;
    for (const task of tasks) {
      const start = task.startedOn ? parseDateOnly(task.startedOn) : null;
      const end = task.dueOn ? parseDateOnly(task.dueOn) : null;
      if (!start && !end) {
        un += 1;
        continue;
      }
      const s = start ?? end;
      const e = end ?? start;
      if (!s || !e) {
        un += 1;
        continue;
      }
      sc.push({ task, start: s, end: e });
    }
    sc.sort((a, b) => {
      const sa = a.start.getTime();
      const sb = b.start.getTime();
      if (sa !== sb) return sa - sb;
      return b.task.priority - a.task.priority;
    });
    return { scheduled: sc, unscheduled: un };
  }, [tasks]);

  const today = startOfDay(new Date());

  const range = useMemo(() => {
    if (scheduled.length === 0) {
      const start = addDays(today, -7);
      const end = addDays(today, 21);
      return { start, end };
    }
    let min = scheduled[0]?.start ?? today;
    let max = scheduled[0]?.end ?? today;
    for (const s of scheduled) {
      if (s.start < min) min = s.start;
      if (s.end > max) max = s.end;
    }
    return { start: addDays(min, -3), end: addDays(max, 3) };
  }, [scheduled, today]);

  const totalDays = Math.max(7, diffDays(range.start, range.end) + 1);
  const viewStart = addDays(range.start, offsetDays);
  const visibleDays = Math.min(totalDays, 42); // ~6 weeks visible
  const chartWidth = visibleDays * DAY_WIDTH;
  const chartHeight = HEADER_HEIGHT + scheduled.length * (ROW_HEIGHT + ROW_GAP);

  const todayOffset = diffDays(viewStart, today);
  const showTodayLine = todayOffset >= 0 && todayOffset < visibleDays;

  /**
   * taskId → on-screen bar coordinates. `x1` is the bar's left edge
   * (start), `x2` its right edge (end), `y` its vertical center. Used
   * both by the bar loop and the dependency arrow layer so that the
   * two stay pixel-aligned.
   */
  const barPositions = useMemo(() => {
    const map = new Map<string, { x1: number; x2: number; y: number }>();
    scheduled.forEach(({ task, start, end }, idx) => {
      const startX = diffDays(viewStart, start) * DAY_WIDTH;
      const endX = (diffDays(viewStart, end) + 1) * DAY_WIDTH;
      const y =
        HEADER_HEIGHT + idx * (ROW_HEIGHT + ROW_GAP) + ROW_GAP / 2 + (ROW_HEIGHT - ROW_GAP) / 2;
      map.set(task.id, { x1: startX, x2: endX, y });
    });
    return map;
  }, [scheduled, viewStart]);

  /**
   * Dependency arrows to draw: for every `blocks` edge where both
   * endpoints are scheduled, draw an orthogonal path from the source
   * bar's right edge to the target bar's left edge. Same-row edges
   * are skipped (they'd overlap the bar itself).
   */
  const dependencyArrows = useMemo(() => {
    const arrows: {
      id: string;
      path: string;
      danger: boolean;
    }[] = [];
    for (const edge of edges) {
      if (edge.kind !== 'blocks') continue;
      const from = barPositions.get(edge.fromTaskId);
      const to = barPositions.get(edge.toTaskId);
      if (!from || !to) continue;
      if (from.y === to.y) continue;
      const sx = from.x2;
      const sy = from.y;
      const tx = to.x1;
      const ty = to.y;
      // Orthogonal elbow: right 8px from source, vertical, left into target.
      const midX = Math.max(sx + 8, tx - 8);
      const path = `M ${sx} ${sy} L ${midX} ${sy} L ${midX} ${ty} L ${tx} ${ty}`;
      const danger =
        edge.fromTaskDerivedState !== 'done' && edge.fromTaskDerivedState !== 'cancelled';
      arrows.push({ id: edge.id, path, danger });
    }
    return arrows;
  }, [edges, barPositions]);

  // Build day cells (for header + grid lines).
  const dayCells: { date: Date; x: number; isMonthStart: boolean; isWeekend: boolean }[] = [];
  for (let i = 0; i < visibleDays; i++) {
    const d = addDays(viewStart, i);
    dayCells.push({
      date: d,
      x: i * DAY_WIDTH,
      isMonthStart: d.getDate() === 1,
      isWeekend: d.getDay() === 0 || d.getDay() === 6,
    });
  }

  const goPrev = (): void => {
    setOffsetDays((o) => Math.max(0, o - 7));
  };
  const goNext = (): void => {
    setOffsetDays((o) => Math.min(Math.max(0, totalDays - visibleDays), o + 7));
  };
  const goToday = (): void => {
    const t0 = diffDays(range.start, today) - Math.floor(visibleDays / 2);
    setOffsetDays(Math.max(0, Math.min(Math.max(0, totalDays - visibleDays), t0)));
  };

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <header
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: '0.75rem',
        }}
      >
        <h1 style={{ margin: 0, fontSize: '1.5rem', fontWeight: 600 }}>{t('gantt.title')}</h1>
        <div style={{ display: 'flex', gap: '0.25rem', alignItems: 'center' }}>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-label={t('gantt.prev')}
            onClick={goPrev}
          >
            <ChevronLeft size={16} aria-hidden />
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={goToday}>
            {t('gantt.today')}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-label={t('gantt.next')}
            onClick={goNext}
          >
            <ChevronRight size={16} aria-hidden />
          </Button>
        </div>
      </header>

      {scheduled.length === 0 ? (
        <div
          style={{
            padding: '3rem 1rem',
            textAlign: 'center',
            color: 'var(--nf-color-fg-muted)',
            border: '1px dashed var(--nf-color-border)',
            borderRadius: '0.75rem',
            background: 'var(--nf-color-bg-sunken)',
          }}
        >
          {t('gantt.empty')}
        </div>
      ) : (
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: `${LABEL_WIDTH}px minmax(0, 1fr)`,
            border: '1px solid var(--color-border, rgba(127,127,127,0.2))',
            borderRadius: '0.5rem',
            overflow: 'hidden',
            background: 'var(--color-surface, rgba(127,127,127,0.04))',
          }}
        >
          {/* Label column */}
          <div
            style={{
              borderInlineEnd: '1px solid var(--color-border, rgba(127,127,127,0.2))',
            }}
          >
            <div
              style={{
                blockSize: HEADER_HEIGHT,
                borderBlockEnd: '1px solid var(--color-border, rgba(127,127,127,0.2))',
              }}
            />
            {scheduled.map(({ task }) => (
              <button
                key={task.id}
                type="button"
                onClick={() => {
                  void navigate({ to: '/tasks/$taskId', params: { taskId: task.id } });
                }}
                title={task.title}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '0.5rem',
                  inlineSize: '100%',
                  blockSize: ROW_HEIGHT + ROW_GAP,
                  paddingInline: '0.625rem',
                  border: 'none',
                  background: 'transparent',
                  textAlign: 'start',
                  color: 'inherit',
                  font: 'inherit',
                  cursor: 'pointer',
                  overflow: 'hidden',
                }}
              >
                <span
                  aria-hidden
                  style={{
                    inlineSize: '0.5rem',
                    blockSize: '0.5rem',
                    borderRadius: '999px',
                    background: STATE_COLOR[task.derivedState] ?? 'var(--color-muted)',
                    flexShrink: 0,
                  }}
                />
                <span
                  style={{
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                    fontSize: '0.8125rem',
                  }}
                >
                  {task.title}
                </span>
              </button>
            ))}
          </div>

          {/* Chart column */}
          <div style={{ overflowX: 'auto' }}>
            <svg
              role="img"
              aria-label={t('gantt.title')}
              width={chartWidth}
              height={chartHeight}
              style={{ display: 'block' }}
            >
              <title>{t('gantt.title')}</title>
              <defs>
                <marker
                  id="gantt-arrow-open"
                  viewBox="0 0 10 10"
                  refX="9"
                  refY="5"
                  markerWidth="6"
                  markerHeight="6"
                  orient="auto-start-reverse"
                >
                  <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--nf-color-danger, #c0392b)" />
                </marker>
                <marker
                  id="gantt-arrow-done"
                  viewBox="0 0 10 10"
                  refX="9"
                  refY="5"
                  markerWidth="6"
                  markerHeight="6"
                  orient="auto-start-reverse"
                >
                  <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--color-muted, #95a5a6)" />
                </marker>
              </defs>
              {/* Day grid + weekend shading */}
              {dayCells.map((c) => (
                <g key={c.date.toISOString()}>
                  {c.isWeekend ? (
                    <rect
                      x={c.x}
                      y={HEADER_HEIGHT}
                      width={DAY_WIDTH}
                      height={chartHeight - HEADER_HEIGHT}
                      fill="rgba(127,127,127,0.06)"
                    />
                  ) : null}
                  <line
                    x1={c.x}
                    y1={HEADER_HEIGHT}
                    x2={c.x}
                    y2={chartHeight}
                    stroke="rgba(127,127,127,0.12)"
                    strokeWidth={c.isMonthStart ? 1.5 : 0.5}
                  />
                  <text
                    x={c.x + DAY_WIDTH / 2}
                    y={HEADER_HEIGHT - 6}
                    textAnchor="middle"
                    fontSize="10"
                    fill="var(--color-muted)"
                  >
                    {c.date.getDate()}
                  </text>
                  {c.isMonthStart ? (
                    <text x={c.x + 4} y={14} fontSize="10" fontWeight="600" fill="var(--color-fg)">
                      {`${c.date.getFullYear()}/${c.date.getMonth() + 1}`}
                    </text>
                  ) : null}
                </g>
              ))}

              {/* Today vertical line */}
              {showTodayLine ? (
                <line
                  x1={todayOffset * DAY_WIDTH + DAY_WIDTH / 2}
                  y1={HEADER_HEIGHT}
                  x2={todayOffset * DAY_WIDTH + DAY_WIDTH / 2}
                  y2={chartHeight}
                  stroke="var(--color-accent, #9b59b6)"
                  strokeWidth={1.5}
                  strokeDasharray="3 3"
                />
              ) : null}

              {/* Bars */}
              {scheduled.map(({ task, start, end }, idx) => {
                const startX = diffDays(viewStart, start) * DAY_WIDTH;
                const endX = (diffDays(viewStart, end) + 1) * DAY_WIDTH;
                const visStart = Math.max(0, startX);
                const visEnd = Math.min(chartWidth, endX);
                if (visEnd <= 0 || visStart >= chartWidth) return null;
                const w = Math.max(2, visEnd - visStart);
                const y = HEADER_HEIGHT + idx * (ROW_HEIGHT + ROW_GAP) + ROW_GAP / 2;
                const fill = STATE_COLOR[task.derivedState] ?? 'var(--color-muted)';
                return (
                  <g key={task.id}>
                    <rect
                      x={visStart}
                      y={y}
                      width={w}
                      height={ROW_HEIGHT - ROW_GAP}
                      rx={4}
                      ry={4}
                      fill={fill}
                      fillOpacity={task.derivedState === 'done' ? 0.5 : 0.85}
                      stroke={fill}
                      strokeWidth={1}
                    >
                      <title>{task.title}</title>
                    </rect>
                  </g>
                );
              })}

              {/* Dependency arrows (drawn last so they sit over the bars) */}
              {dependencyArrows.map((arrow) => (
                <path
                  key={arrow.id}
                  d={arrow.path}
                  fill="none"
                  stroke={
                    arrow.danger ? 'var(--nf-color-danger, #c0392b)' : 'var(--color-muted, #95a5a6)'
                  }
                  strokeWidth={1.25}
                  strokeDasharray={arrow.danger ? undefined : '3 3'}
                  markerEnd={arrow.danger ? 'url(#gantt-arrow-open)' : 'url(#gantt-arrow-done)'}
                  opacity={arrow.danger ? 0.9 : 0.55}
                />
              ))}
            </svg>
          </div>
        </div>
      )}

      {unscheduled > 0 ? (
        <div
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '0.375rem',
            paddingBlock: '0.25rem',
            paddingInline: '0.625rem',
            borderRadius: '999px',
            border: '1px solid var(--nf-color-border)',
            background: 'var(--nf-color-bg-sunken)',
            color: 'var(--nf-color-fg-muted)',
            fontSize: '0.8125rem',
            alignSelf: 'flex-start',
          }}
        >
          {t('gantt.unscheduled', { count: unscheduled })}
        </div>
      ) : null}
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/projects/$projectId/gantt')({
  component: GanttRoute,
});
