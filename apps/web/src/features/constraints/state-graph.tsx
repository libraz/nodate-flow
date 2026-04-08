/**
 * StateGraph — 3.WEB-2 minimal directed-graph visualisation of the
 * task state machine. SVG-based with a fixed hand-laid layout (5 nodes
 * only) so we don't need a graph library. The `current` prop
 * highlights the active derived_state.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

const STATES = ['open', 'waiting', 'review', 'done', 'cancelled'] as const;
export type GraphState = (typeof STATES)[number];

const POSITIONS: Record<GraphState, { x: number; y: number }> = {
  open: { x: 60, y: 120 },
  waiting: { x: 180, y: 60 },
  review: { x: 300, y: 120 },
  done: { x: 420, y: 60 },
  cancelled: { x: 420, y: 180 },
};

const EDGES: { from: GraphState; to: GraphState; label: string }[] = [
  { from: 'open', to: 'waiting', label: 'start' },
  { from: 'waiting', to: 'open', label: 'block' },
  { from: 'waiting', to: 'review', label: 'submit' },
  { from: 'review', to: 'waiting', label: 'reopen' },
  { from: 'review', to: 'done', label: 'complete' },
  { from: 'open', to: 'done', label: 'complete' },
  { from: 'done', to: 'waiting', label: 'reopen' },
  { from: 'open', to: 'cancelled', label: 'cancel' },
  { from: 'waiting', to: 'cancelled', label: 'cancel' },
  { from: 'review', to: 'cancelled', label: 'cancel' },
  { from: 'cancelled', to: 'open', label: 'reopen' },
];

/**
 * External signal sources that may feed this task. Rendered as
 * floating square nodes with a dashed edge into `open`, so the
 * Phase 4 state graph (4.WEB-2) shows clearly that github / slack /
 * google can drive transitions from outside.
 */
const EXTERNAL_SOURCES: { id: string; label: string; color: string; y: number }[] = [
  { id: 'github', label: 'github', color: '#6e5494', y: 30 },
  { id: 'slack', label: 'slack', color: '#4a154b', y: 120 },
  { id: 'google', label: 'google', color: '#4285f4', y: 210 },
];

export interface StateGraphProps {
  current?: string;
  /**
   * Set of external source ids that have actually fired a signal on
   * this task. Active sources render solid; inactive ones render at
   * 40% opacity so the operator can see at a glance which feeds are
   * live.
   */
  activeSources?: ReadonlySet<string>;
}

export default function StateGraph({ current, activeSources }: StateGraphProps): ReactElement {
  const { t } = useTranslation('constraints');
  const openPos = POSITIONS.open;
  return (
    <figure aria-label={t('stateGraph.title')}>
      <figcaption>{t('stateGraph.title')}</figcaption>
      <svg role="img" aria-label={t('stateGraph.legend')} viewBox="-90 0 600 240" width="100%">
        <defs>
          <marker id="arrow" markerWidth="8" markerHeight="8" refX="8" refY="4" orient="auto">
            <path d="M0,0 L8,4 L0,8 Z" fill="currentColor" />
          </marker>
        </defs>
        {EXTERNAL_SOURCES.map((src) => {
          const active = activeSources?.has(src.id) ?? false;
          const x = -70;
          return (
            <g key={src.id} opacity={active ? 1 : 0.4}>
              <line
                x1={x + 36}
                y1={src.y}
                x2={openPos.x - 22}
                y2={openPos.y}
                stroke={src.color}
                strokeDasharray="4 3"
                strokeWidth={1}
                markerEnd="url(#arrow)"
              />
              <rect
                x={x}
                y={src.y - 14}
                width={56}
                height={28}
                rx={4}
                fill="transparent"
                stroke={src.color}
                strokeWidth={1}
              />
              <text x={x + 28} y={src.y + 4} fontSize="10" textAnchor="middle" fill={src.color}>
                {src.label}
              </text>
            </g>
          );
        })}
        {EDGES.map((e, i) => {
          const a = POSITIONS[e.from];
          const b = POSITIONS[e.to];
          return (
            <g key={`${e.from}-${e.to}-${i}`} stroke="currentColor" fill="none">
              <line x1={a.x} y1={a.y} x2={b.x} y2={b.y} markerEnd="url(#arrow)" strokeWidth={1} />
              <text
                x={(a.x + b.x) / 2}
                y={(a.y + b.y) / 2 - 4}
                fontSize="9"
                fill="currentColor"
                stroke="none"
                textAnchor="middle"
              >
                {e.label}
              </text>
            </g>
          );
        })}
        {STATES.map((s) => {
          const p = POSITIONS[s];
          const active = s === current;
          return (
            <g key={s}>
              <circle
                cx={p.x}
                cy={p.y}
                r={22}
                fill={active ? 'currentColor' : 'transparent'}
                stroke="currentColor"
                strokeWidth={active ? 2 : 1}
              />
              <text
                x={p.x}
                y={p.y + 4}
                fontSize="11"
                textAnchor="middle"
                fill={active ? 'var(--nf-bg, white)' : 'currentColor'}
              >
                {s}
              </text>
            </g>
          );
        })}
      </svg>
    </figure>
  );
}
