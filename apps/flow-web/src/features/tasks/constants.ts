/**
 * Shared UI constants for the tasks feature.
 *
 * These maps are used across task list, board, card, detail, spreadsheet,
 * calendar, gantt, and filter components to render priority / state labels,
 * colors, and badge tones consistently.
 */

import type { BadgeTone } from '@nodate-flow/ui';

import type { TaskDerivedState, TaskPriority } from './api';

/* ── Priority ────────────────────────────────────────────────── */

/** i18n key for each priority level. */
export const PRIORITY_KEY: Record<TaskPriority, string> = {
  0: 'tasks.priority.none',
  1: 'tasks.priority.low',
  2: 'tasks.priority.medium',
  3: 'tasks.priority.high',
  4: 'tasks.priority.urgent',
};

/** CSS color string for each priority level. */
export const PRIORITY_COLOR: Record<TaskPriority, string> = {
  0: 'var(--color-muted, #95a5a6)',
  1: '#3498db',
  2: '#e67e22',
  3: '#e74c3c',
  4: '#c0392b',
};

/** Badge tone for each priority level. */
export const PRIORITY_TONE: Record<TaskPriority, BadgeTone> = {
  0: 'neutral',
  1: 'info',
  2: 'accent',
  3: 'warning',
  4: 'danger',
};

/* ── Derived State ───────────────────────────────────────────── */

/** i18n key for each derived state. */
export const STATE_KEY: Record<TaskDerivedState, string> = {
  open: 'tasks.status.open',
  waiting: 'tasks.status.waiting',
  review: 'tasks.status.review',
  done: 'tasks.status.done',
  cancelled: 'tasks.status.cancelled',
};

/** CSS color string for each derived state (with CSS variable fallbacks). */
export const STATE_COLOR: Record<TaskDerivedState, string> = {
  open: 'var(--color-info, #3498db)',
  waiting: 'var(--color-warning, #f39c12)',
  review: 'var(--color-accent, #9b59b6)',
  done: 'var(--color-success, #27ae60)',
  cancelled: 'var(--color-muted, #95a5a6)',
};

/** Badge tone for each derived state. */
export const STATE_TONE: Record<TaskDerivedState, BadgeTone> = {
  open: 'info',
  waiting: 'warning',
  review: 'accent',
  done: 'success',
  cancelled: 'neutral',
};
