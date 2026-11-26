/**
 * Event pill building blocks shared by the calendar grid, month chips,
 * day sheet and the label preview. The colour reaches CSS only as the
 * `--evt` custom property; fill, border and marker derive from it in
 * `event-pill.module.css`.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import type { CSSProperties, ReactElement } from 'react';

import styles from './event-pill.module.css';

export type EventPillKind = 'event' | 'block' | 'free' | 'milestone';

/** Inline style carrying the event colour; nothing else. */
export function eventColorStyle(color: string): CSSProperties {
  return { '--evt': color } as CSSProperties;
}

/** Class names for a pill of the given kind; unknown kinds read as `event`. */
export function eventPillClassName(kind: string): string {
  switch (kind) {
    case 'block':
      return cx(styles.pill, styles.kindBlock);
    case 'free':
      return cx(styles.pill, styles.kindFree);
    case 'milestone':
      return cx(styles.pill, styles.kindMilestone);
    default:
      return cx(styles.pill, styles.kindEvent);
  }
}

/** Class for the diamond marker inside a pill. */
export const eventPillMarkerClassName = styles.marker as string;

/** Class for the kind legend swatch (set `data-kind`). */
export const eventKindSampleClassName = styles.kindSample as string;

export interface EventPillPreviewProps {
  color: string;
  title: string;
  kind?: EventPillKind;
  className?: string;
}

/** A static pill drawn with the same CSS as the calendar's real pills. */
export function EventPillPreview({
  color,
  title,
  kind = 'event',
  className,
}: EventPillPreviewProps): ReactElement {
  return (
    <div
      data-testid="event-pill-preview"
      className={cx(eventPillClassName(kind), styles.preview, className)}
      style={eventColorStyle(color)}
    >
      <span aria-hidden="true" className={styles.marker} />
      <span className={styles.previewTitle}>{title}</span>
    </div>
  );
}
