/**
 * Shared UI constants for the tasks feature.
 *
 * These maps are used across task list, board, card, detail, spreadsheet,
 * calendar, gantt, and filter components to render priority / state labels,
 * colors, and badge tones consistently.
 */

import type { BadgeTone } from '@nodate-flow/ui/primitives/badge';

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
  0: 'var(--nf-color-fg-muted)',
  1: 'var(--nf-color-info)',
  2: 'var(--nf-color-warning)',
  3: 'var(--nf-color-danger)',
  4: 'var(--nf-color-danger)',
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

/**
 * Fill colour for each derived state — the dot beside the label, and the
 * wash behind it. Bright enough to read as the state at a glance, which
 * is the opposite of what text needs.
 */
export const STATE_COLOR: Record<TaskDerivedState, string> = {
  open: 'var(--nf-color-info)',
  waiting: 'var(--nf-color-warning)',
  review: 'var(--nf-color-accent)',
  done: 'var(--nf-color-success)',
  cancelled: 'var(--nf-color-fg-muted)',
};

/**
 * Text colour for each derived state. The fills above sit between 1.9:1
 * and 4.0:1 against the page background, so a label painted with one is
 * somewhere between hard and impossible to read; the `-fg` counterparts
 * are 7.3:1 or better in every theme.
 *
 * `review` is the exception: there is no `--nf-color-accent-fg`, so it
 * keeps the accent and stays at the accent's own contrast.
 */
export const STATE_TEXT_COLOR: Record<TaskDerivedState, string> = {
  open: 'var(--nf-color-info-fg)',
  waiting: 'var(--nf-color-warning-fg)',
  review: 'var(--nf-color-accent)',
  done: 'var(--nf-color-success-fg)',
  cancelled: 'var(--nf-color-fg-muted)',
};

/** Badge tone for each derived state. */
export const STATE_TONE: Record<TaskDerivedState, BadgeTone> = {
  open: 'info',
  waiting: 'warning',
  review: 'accent',
  done: 'success',
  cancelled: 'neutral',
};
