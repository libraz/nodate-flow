/**
 * /workspaces/$id/projects/$projectId/gantt — lightweight self-rendered
 * Gantt view.
 *
 * Read-only observation view. Bars are drawn as plain SVG between
 * `started_on` and `due_on`. Tasks missing both dates are listed as
 * "unscheduled" and excluded from the chart. Click a bar or row label
 * to drill into the task detail panel.
 *
 * Features:
 * - Zoom: day / week / month presets + step zoom in/out
 * - Critical path: longest dependency chain highlighted in red
 * - Dependency arrows: blocks edges with arrowheads
 */

import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import SegmentedControl from '@nodate-flow/ui/primitives/segmented-control';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { ToggleChip } from '@nodate-flow/ui/primitives/toggle-chip';
import { useSuspenseQuery } from '@tanstack/react-query';
import { createLazyFileRoute, getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { ChevronLeft, ChevronRight, Minus, Plus } from 'lucide-react';
import { type ReactElement, Suspense, useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useProjectDependenciesQuery } from '../features/projects/api';
import type { TaskDerivedState } from '../features/tasks/api';
import { STATE_COLOR } from '../features/tasks/constants';
import { sdk } from '../lib/sdk';

type TaskListItem = components['schemas']['TaskListItem'];

const routeApi = getRouteApi('/_authenticated/workspaces/$id/projects/$projectId/gantt');

const ROW_HEIGHT = 28;
const ROW_GAP = 4;
const BAR_HEIGHT = 14;
const HEADER_HEIGHT = 36;
const LABEL_WIDTH = 220;

/**
 * Shared accent color for the critical-path bar stroke, dependency arrows
 * and the `ToggleChip` legend. Kept as a single source of truth so the
 * toggle visually doubles as a legend for the chart highlight.
 */
const CRITICAL_PATH_COLOR = 'var(--nf-color-danger)';

/** Zoom presets: pixel width per day. */
const ZOOM_LEVELS = [4, 6, 8, 12, 18, 24, 36, 48, 64] as const;
const ZOOM_PRESET_DAY = 6; // index → 48px
const ZOOM_PRESET_WEEK = 4; // index → 18px
const ZOOM_PRESET_MONTH = 1; // index → 6px
const ZOOM_DEFAULT = ZOOM_PRESET_WEEK; // start at the Week preset so the button is highlighted

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

interface DependencyEdge {
  id: string;
  kind: string;
  fromTaskId: string;
  toTaskId: string;
  fromTaskDerivedState?: string;
}

/**
 * Compute the critical path: the longest chain of `blocks` edges
 * weighted by bar duration (in days). Returns the set of task IDs
 * on the critical path.
 */
function computeCriticalPath(
  scheduled: readonly ScheduledTask[],
  edges: readonly DependencyEdge[],
): Set<string> {
  const taskMap = new Map<string, ScheduledTask>();
  for (const s of scheduled) taskMap.set(s.task.id, s);

  // Build adjacency: blocker → [blocked]
  const adj = new Map<string, string[]>();
  const inDegree = new Map<string, number>();
  for (const s of scheduled) {
    adj.set(s.task.id, []);
    inDegree.set(s.task.id, 0);
  }
  for (const edge of edges) {
    if (edge.kind !== 'blocks') continue;
    if (!taskMap.has(edge.fromTaskId) || !taskMap.has(edge.toTaskId)) continue;
    adj.get(edge.fromTaskId)?.push(edge.toTaskId);
    inDegree.set(edge.toTaskId, (inDegree.get(edge.toTaskId) ?? 0) + 1);
  }

  // Longest-path via topological order (DAG assumed)
  const dist = new Map<string, number>();
  const prev = new Map<string, string | null>();
  for (const s of scheduled) {
    const dur = Math.max(1, diffDays(s.start, s.end) + 1);
    dist.set(s.task.id, dur);
    prev.set(s.task.id, null);
  }

  // Kahn's algorithm for topo sort
  const queue: string[] = [];
  for (const [id, deg] of inDegree) {
    if (deg === 0) queue.push(id);
  }
  const order: string[] = [];
  while (queue.length > 0) {
    const u = queue.shift();
    if (!u) break;
    order.push(u);
    for (const v of adj.get(u) ?? []) {
      const vTask = taskMap.get(v);
      const vDur = vTask ? Math.max(1, diffDays(vTask.start, vTask.end) + 1) : 1;
      const newDist = (dist.get(u) ?? 0) + vDur;
      if (newDist > (dist.get(v) ?? 0)) {
        dist.set(v, newDist);
        prev.set(v, u);
      }
      const d = (inDegree.get(v) ?? 1) - 1;
      inDegree.set(v, d);
      if (d === 0) queue.push(v);
    }
  }

  // Find the endpoint with the longest distance
  let maxDist = 0;
  let endId: string | null = null;
  for (const [id, d] of dist) {
    if (d > maxDist) {
      maxDist = d;
      endId = id;
    }
  }

  // Only show critical path if it spans at least 2 tasks
  const path = new Set<string>();
  let cur = endId;
  while (cur !== null) {
    path.add(cur);
    cur = prev.get(cur) ?? null;
  }
  if (path.size < 2) return new Set();
  return path;
}

/** Symbolic identifier for each zoom preset used by the segmented control. */
type ZoomPreset = 'month' | 'week' | 'day';

/** Map a preset identifier to its `ZOOM_LEVELS` index. */
const ZOOM_PRESET_INDEX: Record<ZoomPreset, number> = {
  month: ZOOM_PRESET_MONTH,
  week: ZOOM_PRESET_WEEK,
  day: ZOOM_PRESET_DAY,
};

/** Inverse of {@link ZOOM_PRESET_INDEX}: map a zoom index back to a preset. */
function zoomPresetFromIndex(idx: number): ZoomPreset | undefined {
  if (idx === ZOOM_PRESET_MONTH) return 'month';
  if (idx === ZOOM_PRESET_WEEK) return 'week';
  if (idx === ZOOM_PRESET_DAY) return 'day';
  return undefined;
}

function GanttRoute(): ReactElement {
  return (
    // nf-token-override: placeholder sized to the content it stands in for, not a spacing step
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
  const [zoomIdx, setZoomIdx] = useState(ZOOM_DEFAULT);
  const [showCriticalPath, setShowCriticalPath] = useState(true);

  const dayWidth = ZOOM_LEVELS[zoomIdx] ?? 24;

  const zoomIn = useCallback(() => {
    setZoomIdx((i) => Math.min(ZOOM_LEVELS.length - 1, i + 1));
  }, []);
  const zoomOut = useCallback(() => {
    setZoomIdx((i) => Math.max(0, i - 1));
  }, []);

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

  const { scheduled, unscheduledTasks } = useMemo(() => {
    const sc: ScheduledTask[] = [];
    const un: TaskListItem[] = [];
    for (const task of tasks) {
      const start = task.startedOn ? parseDateOnly(task.startedOn) : null;
      const end = task.dueOn ? parseDateOnly(task.dueOn) : null;
      if (!start && !end) {
        un.push(task);
        continue;
      }
      const s = start ?? end;
      const e = end ?? start;
      if (!s || !e) {
        un.push(task);
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
    un.sort((a, b) => b.priority - a.priority);
    return { scheduled: sc, unscheduledTasks: un };
  }, [tasks]);
  const unscheduled = unscheduledTasks.length;

  const criticalPathIds = useMemo(
    () => (showCriticalPath ? computeCriticalPath(scheduled, edges) : new Set<string>()),
    [scheduled, edges, showCriticalPath],
  );

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
  // Visible days: at least the data range, but cap to viewport-driven max
  const viewportDays = Math.max(14, Math.round(960 / dayWidth));
  const visibleDays = Math.min(totalDays - offsetDays, viewportDays);
  const chartWidth = visibleDays * dayWidth;
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
      const startX = diffDays(viewStart, start) * dayWidth;
      const endX = (diffDays(viewStart, end) + 1) * dayWidth;
      const y = HEADER_HEIGHT + idx * (ROW_HEIGHT + ROW_GAP) + (ROW_HEIGHT + ROW_GAP) / 2;
      map.set(task.id, { x1: startX, x2: endX, y });
    });
    return map;
  }, [scheduled, viewStart, dayWidth]);

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
      critical: boolean;
    }[] = [];
    for (const edge of edges) {
      if (edge.kind !== 'blocks') continue;
      const from = barPositions.get(edge.fromTaskId);
      const to = barPositions.get(edge.toTaskId);
      if (!from || !to) continue;
      if (from.y === to.y) continue;
      // Offset endpoints so arrows start/end outside the bars.
      // Arrows always arrive at the target's left edge going rightward (→).
      const sx = from.x2 + 2;
      const sy = from.y;
      const tx = to.x1 - 6; // leave room for the arrowhead marker
      const ty = to.y;
      let path: string;
      // Half-row offset used to reach the gap between adjacent rows.
      const rowStep = (ROW_HEIGHT + ROW_GAP) / 2;
      if (sx + 12 <= tx) {
        // Simple case: target is far enough to the right.
        // right → down → right (arrives horizontally from the left).
        const midX = sx + 8;
        path = `M ${sx} ${sy} L ${midX} ${sy} L ${midX} ${ty} L ${tx} ${ty}`;
      } else {
        // Target overlaps or is to the left: wrap around so the arrow
        // approaches from the left: right → down(gap) → left → down(gap) → right.
        // Horizontal segments run through row gaps, not through bars.
        const exitX = sx + 8;
        const entryX = Math.min(from.x1, to.x1) - 10;
        const gapAfterSource = sy + (ty > sy ? rowStep : -rowStep);
        // Left vertical segment goes all the way to ty, then horizontal right to target.
        path = `M ${sx} ${sy} L ${exitX} ${sy} L ${exitX} ${gapAfterSource} L ${entryX} ${gapAfterSource} L ${entryX} ${ty} L ${tx} ${ty}`;
      }
      const danger =
        edge.fromTaskDerivedState !== 'done' && edge.fromTaskDerivedState !== 'cancelled';
      const critical = criticalPathIds.has(edge.fromTaskId) && criticalPathIds.has(edge.toTaskId);
      arrows.push({ id: edge.id, path, danger, critical });
    }
    return arrows;
  }, [edges, barPositions, criticalPathIds]);

  // Build day cells (for header + grid lines).
  const dayCells: { date: Date; x: number; isMonthStart: boolean; isWeekend: boolean }[] = [];
  for (let i = 0; i < visibleDays; i++) {
    const d = addDays(viewStart, i);
    dayCells.push({
      date: d,
      x: i * dayWidth,
      isMonthStart: d.getDate() === 1,
      isWeekend: d.getDay() === 0 || d.getDay() === 6,
    });
  }

  // Show day number in header only when there's enough room
  const showDayLabel = dayWidth >= 12;
  // Always show the month/year of the first visible day when no month
  // boundary is in view, so users always have date context.
  const hasMonthBoundary = dayCells.some((c) => c.isMonthStart);
  const firstDayDate = dayCells[0]?.date ?? viewStart;

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
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}>
      <style>{`
        .gantt-label-row:hover { background: var(--nf-color-bg-sunken); }
        .gantt-label-row:focus-visible { outline: 2px solid var(--nf-color-accent); outline-offset: -2px; }
      `}</style>
      <header
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 'var(--nf-space-3)',
          flexWrap: 'wrap',
        }}
      >
        <h2 style={{ margin: 0, fontSize: 'var(--nf-text-2xl)', fontWeight: 600 }}>
          {t('gantt.title')}
        </h2>
        <div
          style={{
            display: 'flex',
            gap: 'var(--nf-space-2)',
            alignItems: 'center',
            flexWrap: 'wrap',
          }}
        >
          {/* Navigation */}
          <div style={{ display: 'flex', gap: 'var(--nf-space-1)', alignItems: 'center' }}>
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

          {/* Zoom controls */}
          <div
            style={{
              display: 'flex',
              gap: '0.125rem',
              alignItems: 'center',
              borderInlineStart: '1px solid var(--nf-color-border)',
              paddingInlineStart: 'var(--nf-space-2)',
            }}
          >
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-label={t('gantt.zoom_out')}
              onClick={zoomOut}
              disabled={zoomIdx === 0}
            >
              <Minus size={14} aria-hidden />
            </Button>
            {/*
             * View selector: Month / Week / Day as a segmented control.
             * Delegates to the shared `SegmentedControl` primitive which
             * implements the WAI-ARIA APG radiogroup pattern (roving
             * tabindex, arrow / Home / End nav, space / enter activation).
             * When the current zoom index doesn't match any preset (the
             * user has stepped in/out) the control renders with no segment
             * selected, which the primitive handles by parking roving
             * focus on the first enabled option.
             */}
            <SegmentedControl<ZoomPreset>
              ariaLabel={t('gantt.zoom_preset_aria')}
              size="sm"
              value={zoomPresetFromIndex(zoomIdx) ?? 'week'}
              onChange={(next) => setZoomIdx(ZOOM_PRESET_INDEX[next])}
              options={[
                { value: 'month', label: t('gantt.zoom_month') },
                { value: 'week', label: t('gantt.zoom_week') },
                { value: 'day', label: t('gantt.zoom_day') },
              ]}
            />
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-label={t('gantt.zoom_in')}
              onClick={zoomIn}
              disabled={zoomIdx === ZOOM_LEVELS.length - 1}
            >
              <Plus size={14} aria-hidden />
            </Button>
          </div>

          {/* Critical path toggle (doubles as a legend: accent matches bar stroke). */}
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              borderInlineStart: '1px solid var(--nf-color-border)',
              paddingInlineStart: 'var(--nf-space-2)',
            }}
          >
            <ToggleChip
              pressed={showCriticalPath}
              onPressedChange={setShowCriticalPath}
              color={CRITICAL_PATH_COLOR}
            >
              {t('gantt.critical_path')}
            </ToggleChip>
          </div>
        </div>
      </header>

      {scheduled.length === 0 ? (
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
          {t('gantt.empty')}
        </div>
      ) : (
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: `${LABEL_WIDTH}px minmax(0, 1fr)`,
            border: '1px solid var(--nf-color-border)',
            borderRadius: 'var(--nf-radius-md)',
            overflow: 'hidden',
            background: 'var(--nf-color-surface)',
          }}
        >
          {/* Label column */}
          <div
            style={{
              borderInlineEnd: '1px solid var(--nf-color-border)',
            }}
          >
            <div
              style={{
                blockSize: HEADER_HEIGHT,
                borderBlockEnd: '1px solid var(--nf-color-border)',
              }}
            />
            {scheduled.map(({ task }) => (
              <Link
                key={task.id}
                to="/tasks/$taskId"
                params={{ taskId: task.id }}
                title={task.title}
                className="gantt-label-row"
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--nf-space-2)',
                  inlineSize: '100%',
                  blockSize: ROW_HEIGHT + ROW_GAP,
                  paddingInline: '0.625rem',
                  textAlign: 'start',
                  color: 'inherit',
                  textDecoration: 'none',
                  overflow: 'hidden',
                }}
              >
                <span
                  aria-hidden
                  style={{
                    // nf-token-override: component dimension, not a spacing step
                    inlineSize: '0.5rem',
                    // nf-token-override: component dimension, not a spacing step
                    blockSize: '0.5rem',
                    borderRadius: 'var(--nf-radius-pill)',
                    background:
                      STATE_COLOR[task.derivedState as TaskDerivedState] ??
                      'var(--nf-color-fg-muted)',
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
              </Link>
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
                  markerWidth="4"
                  markerHeight="4"
                  orient="auto-start-reverse"
                >
                  <path d="M 0 0 L 10 5 L 0 10 z" fill={CRITICAL_PATH_COLOR} />
                </marker>
                <marker
                  id="gantt-arrow-done"
                  viewBox="0 0 10 10"
                  refX="9"
                  refY="5"
                  markerWidth="4"
                  markerHeight="4"
                  orient="auto-start-reverse"
                >
                  <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--nf-color-fg-muted)" />
                </marker>
              </defs>
              {/* Day grid + weekend shading */}
              {dayCells.map((c) => (
                <g key={c.date.toISOString()}>
                  {c.isWeekend ? (
                    <rect
                      x={c.x}
                      y={HEADER_HEIGHT}
                      width={dayWidth}
                      height={chartHeight - HEADER_HEIGHT}
                      fill="var(--nf-color-bg-sunken)"
                    />
                  ) : null}
                  <line
                    x1={c.x}
                    y1={HEADER_HEIGHT}
                    x2={c.x}
                    y2={chartHeight}
                    stroke="var(--nf-color-hairline)"
                    strokeWidth={c.isMonthStart ? 1.5 : 0.5}
                  />
                  {showDayLabel ? (
                    <text
                      x={c.x + dayWidth / 2}
                      y={HEADER_HEIGHT - 6}
                      textAnchor="middle"
                      fontSize="10"
                      fill="var(--nf-color-fg-muted)"
                    >
                      {c.date.getDate()}
                    </text>
                  ) : null}
                  {c.isMonthStart ? (
                    <text
                      x={c.x + 4}
                      y={14}
                      fontSize="10"
                      fontWeight="600"
                      fill="var(--nf-color-fg)"
                    >
                      {`${c.date.getFullYear()}/${c.date.getMonth() + 1}`}
                    </text>
                  ) : null}
                </g>
              ))}

              {/* Fallback month label when no boundary is in view */}
              {!hasMonthBoundary ? (
                <text x={4} y={14} fontSize="10" fontWeight="600" fill="var(--nf-color-fg)">
                  {`${firstDayDate.getFullYear()}/${firstDayDate.getMonth() + 1}`}
                </text>
              ) : null}

              {/* Today vertical line */}
              {showTodayLine ? (
                <line
                  x1={todayOffset * dayWidth + dayWidth / 2}
                  y1={HEADER_HEIGHT}
                  x2={todayOffset * dayWidth + dayWidth / 2}
                  y2={chartHeight}
                  stroke="var(--nf-color-accent)"
                  strokeWidth={1.5}
                  strokeDasharray="3 3"
                />
              ) : null}

              {/* Dependency arrows (drawn before bars so they sit behind them) */}
              {dependencyArrows.map((arrow) => (
                <path
                  key={arrow.id}
                  d={arrow.path}
                  fill="none"
                  stroke={
                    arrow.critical
                      ? CRITICAL_PATH_COLOR
                      : arrow.danger
                        ? CRITICAL_PATH_COLOR
                        : 'var(--nf-color-fg-muted)'
                  }
                  strokeWidth={arrow.critical ? 1.5 : 0.75}
                  strokeDasharray={arrow.danger || arrow.critical ? undefined : '3 3'}
                  markerEnd={
                    arrow.danger || arrow.critical
                      ? 'url(#gantt-arrow-open)'
                      : 'url(#gantt-arrow-done)'
                  }
                  opacity={arrow.critical ? 1 : arrow.danger ? 0.9 : 0.55}
                />
              ))}

              {/* Bars (drawn after arrows so they sit on top) */}
              {scheduled.map(({ task, start, end }, idx) => {
                const startX = diffDays(viewStart, start) * dayWidth;
                const endX = (diffDays(viewStart, end) + 1) * dayWidth;
                const visStart = Math.max(0, startX);
                const visEnd = Math.min(chartWidth, endX);
                if (visEnd <= 0 || visStart >= chartWidth) return null;
                const w = Math.max(2, visEnd - visStart);
                const y =
                  HEADER_HEIGHT +
                  idx * (ROW_HEIGHT + ROW_GAP) +
                  (ROW_HEIGHT + ROW_GAP - BAR_HEIGHT) / 2;
                const fill =
                  STATE_COLOR[task.derivedState as TaskDerivedState] ?? 'var(--nf-color-fg-muted)';
                const isCritical = criticalPathIds.has(task.id);
                return (
                  // biome-ignore lint/a11y/useSemanticElements: SVG task bars cannot be native <button> elements; role="button" + tabIndex + keyboard handler is the correct interactive pattern inside the chart.
                  <g
                    key={task.id}
                    role="button"
                    style={{ cursor: 'pointer' }}
                    onClick={() => {
                      void navigate({
                        to: '/tasks/$taskId',
                        params: { taskId: task.id },
                      });
                    }}
                    aria-label={task.title}
                    tabIndex={0}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        void navigate({
                          to: '/tasks/$taskId',
                          params: { taskId: task.id },
                        });
                      }
                    }}
                  >
                    {isCritical ? (
                      <rect
                        x={visStart - 2}
                        y={y - 2}
                        width={w + 4}
                        height={BAR_HEIGHT + 4}
                        rx={6}
                        ry={6}
                        fill="none"
                        stroke={CRITICAL_PATH_COLOR}
                        strokeWidth={2}
                        strokeDasharray="4 2"
                        opacity={0.7}
                      />
                    ) : null}
                    <rect
                      x={visStart}
                      y={y}
                      width={w}
                      height={BAR_HEIGHT}
                      rx={3}
                      ry={3}
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
            </svg>
          </div>
        </div>
      )}

      {unscheduled > 0 ? (
        <details
          style={{
            alignSelf: 'stretch',
            border: '1px solid var(--nf-color-border)',
            borderRadius: 'var(--nf-radius-md)',
            background: 'var(--nf-color-bg-sunken)',
            padding: 'var(--nf-space-2) var(--nf-space-3)',
          }}
        >
          <summary
            style={{
              cursor: 'pointer',
              fontSize: '0.8125rem',
              color: 'var(--nf-color-fg-muted)',
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--nf-space-2)',
            }}
          >
            {t('gantt.unscheduled', { count: unscheduled })}
          </summary>
          <ul
            style={{
              listStyle: 'none',
              margin: 0,
              padding: 0,
              paddingBlockStart: '0.625rem',
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--nf-space-1)',
            }}
          >
            {unscheduledTasks.map((task) => (
              <li key={task.id}>
                <Link
                  to="/tasks/$taskId"
                  params={{ taskId: task.id }}
                  style={{
                    inlineSize: '100%',
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--nf-space-2)',
                    padding: '0.375rem var(--nf-space-2)',
                    borderRadius: 'var(--nf-radius-sm)',
                    color: 'var(--nf-color-fg)',
                    fontSize: '0.8125rem',
                    textDecoration: 'none',
                  }}
                >
                  <span
                    aria-hidden
                    style={{
                      // nf-token-override: component dimension, not a spacing step
                      inlineSize: '0.5rem',
                      // nf-token-override: component dimension, not a spacing step
                      blockSize: '0.5rem',
                      borderRadius: 'var(--nf-radius-pill)',
                      background:
                        STATE_COLOR[task.derivedState as TaskDerivedState] ??
                        'var(--nf-color-fg-muted)',
                      flexShrink: 0,
                    }}
                  />
                  <span
                    style={{
                      flex: 1,
                      minInlineSize: 0,
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {task.title}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </section>
  );
}

export const Route = createLazyFileRoute(
  '/_authenticated/workspaces/$id/projects/$projectId/gantt',
)({
  component: GanttRoute,
});
