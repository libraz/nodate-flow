/**
 * ProjectStateGraph — project-level directed graph showing all tasks as
 * nodes and their dependency edges. Tasks are grouped by `derivedState`
 * into columns (open, waiting, review, done, cancelled) with curved
 * bezier edges between connected nodes.
 *
 * Uses a layered/grid layout for simplicity and predictability, avoiding
 * force-directed physics. Designed to handle up to 100 tasks smoothly.
 */

import { useNavigate } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useProjectDependenciesQuery } from '../projects/api';
import { TASK_STATES, type TaskDerivedState, type TaskListItem, useTasksQuery } from '../tasks/api';

export interface ProjectStateGraphProps {
  projectId: string;
}

/** State-based fill colours for node backgrounds (light tint). */
const STATE_BG: Record<TaskDerivedState, string> = {
  open: 'oklch(0.92 0.04 250)',
  waiting: 'oklch(0.92 0.06 70)',
  review: 'oklch(0.92 0.05 300)',
  done: 'oklch(0.92 0.05 155)',
  cancelled: 'oklch(0.92 0.02 30)',
};

/** State-based border/stroke colours. */
const STATE_BORDER: Record<TaskDerivedState, string> = {
  open: 'oklch(0.55 0.15 250)',
  waiting: 'oklch(0.55 0.15 70)',
  review: 'oklch(0.55 0.15 300)',
  done: 'oklch(0.55 0.15 155)',
  cancelled: 'oklch(0.55 0.08 30)',
};

/**
 * Resolve a translated column header for each task state.
 * Uses static `t()` calls to satisfy the no-dynamic-key rule.
 */
function stateLabel(state: TaskDerivedState, t: (key: string) => string): string {
  switch (state) {
    case 'open':
      return t('common:tasks.status.open');
    case 'waiting':
      return t('common:tasks.status.waiting');
    case 'review':
      return t('common:tasks.status.review');
    case 'done':
      return t('common:tasks.status.done');
    case 'cancelled':
      return t('common:tasks.status.cancelled');
  }
}

const NODE_WIDTH = 160;
const NODE_HEIGHT = 40;
const COL_GAP = 60;
const ROW_GAP = 16;
const COL_WIDTH = NODE_WIDTH + COL_GAP;
const HEADER_HEIGHT = 36;
const PADDING_X = 24;
const PADDING_Y = 16;
const MAX_TITLE_LEN = 18;

interface NodePosition {
  x: number;
  y: number;
  task: TaskListItem;
}

/**
 * Compute a layered layout: tasks grouped by derivedState into columns,
 * stacked vertically within each column.
 */
function computeLayout(tasks: readonly TaskListItem[]): {
  positions: Map<string, NodePosition>;
  width: number;
  height: number;
} {
  const columns = new Map<TaskDerivedState, TaskListItem[]>();
  for (const s of TASK_STATES) {
    columns.set(s, []);
  }
  for (const task of tasks) {
    const state = task.derivedState as TaskDerivedState;
    const col = columns.get(state);
    if (col) {
      col.push(task);
    } else {
      // Fallback to open if unknown state
      columns.get('open')?.push(task);
    }
  }

  const positions = new Map<string, NodePosition>();
  let maxRows = 0;

  let colIndex = 0;
  for (const state of TASK_STATES) {
    const col = columns.get(state) ?? [];
    if (col.length > maxRows) maxRows = col.length;
    for (let row = 0; row < col.length; row++) {
      const task = col[row];
      if (!task) continue;
      positions.set(task.id, {
        x: PADDING_X + colIndex * COL_WIDTH,
        y: PADDING_Y + HEADER_HEIGHT + row * (NODE_HEIGHT + ROW_GAP),
        task,
      });
    }
    colIndex++;
  }

  const width = PADDING_X * 2 + TASK_STATES.length * COL_WIDTH - COL_GAP;
  const height = PADDING_Y * 2 + HEADER_HEIGHT + Math.max(1, maxRows) * (NODE_HEIGHT + ROW_GAP);

  return { positions, width, height };
}

/** Truncate a title for display inside a node. */
function truncateTitle(title: string): string {
  if (title.length <= MAX_TITLE_LEN) return title;
  return `${title.slice(0, MAX_TITLE_LEN - 1)}\u2026`;
}

/**
 * Build a cubic bezier path from the right edge of the source node to
 * the left edge of the target node. When the target is to the left of
 * the source (back-edges), route the curve around the nodes.
 */
function edgePath(sx: number, sy: number, tx: number, ty: number): string {
  const startX = sx + NODE_WIDTH;
  const startY = sy + NODE_HEIGHT / 2;
  const endX = tx;
  const endY = ty + NODE_HEIGHT / 2;

  if (endX > startX) {
    // Forward edge — simple cubic bezier
    const dx = (endX - startX) / 2;
    return `M${startX},${startY} C${startX + dx},${startY} ${endX - dx},${endY} ${endX},${endY}`;
  }

  // Back-edge — route below/above to avoid crossing nodes
  const offset = 30;
  const midY = Math.max(startY, endY) + NODE_HEIGHT + offset;
  return [
    `M${startX},${startY}`,
    `C${startX + offset},${startY} ${startX + offset},${midY} ${(startX + endX) / 2},${midY}`,
    `C${endX - offset},${midY} ${endX - offset},${endY} ${endX},${endY}`,
  ].join(' ');
}

export default function ProjectStateGraph({ projectId }: ProjectStateGraphProps): ReactElement {
  const { t } = useTranslation(['constraints', 'common']);
  const navigate = useNavigate();
  const { data: tasks } = useTasksQuery(projectId);
  const { data: edges } = useProjectDependenciesQuery(projectId);

  if (tasks.length === 0) {
    return (
      <div
        role="status"
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          minBlockSize: '300px',
          color: 'var(--color-muted)',
        }}
      >
        <p>{t('constraints:stateGraph.noTasks')}</p>
      </div>
    );
  }

  const { positions, width, height } = computeLayout(tasks);

  // Filter edges to only those whose both endpoints exist in the current tasks
  const visibleEdges = edges.filter(
    (e) => positions.has(e.fromTaskId) && positions.has(e.toTaskId),
  );

  function handleNodeClick(taskId: string): void {
    void navigate({
      to: '/tasks/$taskId',
      params: { taskId },
    });
  }

  return (
    <figure
      aria-label={t('constraints:stateGraph.projectTitle')}
      style={{ margin: 0, overflow: 'auto', minBlockSize: '300px' }}
    >
      <figcaption
        style={{
          fontFamily: 'var(--font-display)',
          fontSize: '1rem',
          fontWeight: 600,
          marginBlockEnd: '0.75rem',
        }}
      >
        {t('constraints:stateGraph.projectTitle')}
        {visibleEdges.length === 0 && tasks.length > 0 && (
          <span
            style={{
              fontWeight: 400,
              fontSize: '0.8125rem',
              color: 'var(--color-muted)',
              marginInlineStart: '0.75rem',
            }}
          >
            {t('constraints:stateGraph.noDependencies')}
          </span>
        )}
      </figcaption>
      <svg
        role="img"
        aria-label={t('constraints:stateGraph.projectTitle')}
        viewBox={`0 0 ${width} ${height}`}
        width={width}
        height={height}
        style={{ maxInlineSize: '100%', blockSize: 'auto' }}
      >
        <defs>
          <marker id="dep-arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
            <path d="M0,0 L8,4 L0,8 Z" fill="var(--color-border, #94a3b8)" />
          </marker>
        </defs>

        {/* Column headers */}
        {TASK_STATES.map((state, idx) => (
          <text
            key={state}
            x={PADDING_X + idx * COL_WIDTH + NODE_WIDTH / 2}
            y={PADDING_Y + 14}
            fontSize="12"
            fontWeight="600"
            textAnchor="middle"
            fill={STATE_BORDER[state]}
          >
            {stateLabel(state, t)}
          </text>
        ))}

        {/* Edges */}
        {visibleEdges.map((edge) => {
          const from = positions.get(edge.fromTaskId);
          const to = positions.get(edge.toTaskId);
          if (!from || !to) return null;
          return (
            <path
              key={edge.id}
              d={edgePath(from.x, from.y, to.x, to.y)}
              fill="none"
              stroke="var(--color-border, #94a3b8)"
              strokeWidth={1.5}
              markerEnd="url(#dep-arrow)"
              opacity={0.7}
            />
          );
        })}

        {/* Nodes */}
        {Array.from(positions.values()).map(({ x, y, task }) => {
          const state = task.derivedState as TaskDerivedState;
          return (
            <g
              key={task.id}
              style={{ cursor: 'pointer' }}
              role="button"
              tabIndex={0}
              aria-label={task.title}
              onClick={() => {
                handleNodeClick(task.id);
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  handleNodeClick(task.id);
                }
              }}
            >
              <rect
                x={x}
                y={y}
                width={NODE_WIDTH}
                height={NODE_HEIGHT}
                rx={8}
                fill={STATE_BG[state]}
                stroke={STATE_BORDER[state]}
                strokeWidth={1.5}
              />
              <text
                x={x + NODE_WIDTH / 2}
                y={y + NODE_HEIGHT / 2 + 4}
                fontSize="11"
                textAnchor="middle"
                fill={STATE_BORDER[state]}
              >
                {truncateTitle(task.title)}
              </text>
            </g>
          );
        })}
      </svg>
    </figure>
  );
}
