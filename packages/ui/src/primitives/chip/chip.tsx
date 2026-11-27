/**
 * Chip — small dismissible tag for active filters and selections.
 */

import { X } from 'lucide-react';
import { type CSSProperties, forwardRef, type HTMLAttributes, type ReactElement } from 'react';
import { cx } from '../../lib/cx';
import styles from './chip.module.css';

/** Semantic tone for the Chip. */
export type ChipTone = 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'accent';

/** Chip size. `sm` matches the Badge primitive's height and padding. */
export type ChipSize = 'md' | 'sm';

/** Props for the {@link Chip} component. */
export interface ChipProps extends HTMLAttributes<HTMLSpanElement> {
  /** Semantic tone. Defaults to `"neutral"`. */
  tone?: ChipTone;
  /**
   * Custom CSS colour (any valid `<color>`). Renders a tinted background, a
   * tinted 1px border and a leading dot in this colour; the text keeps the
   * foreground token so arbitrary pastels never become text colour. Overrides
   * `tone`.
   */
  color?: string;
  /** Size. `"sm"` matches Badge's metrics so both sit flush in a row. Defaults to `"md"`. */
  size?: ChipSize;
  /** Called when the dismiss button is clicked. When omitted, no dismiss button is rendered. */
  onDismiss?: () => void;
  /** Accessible label for the dismiss button. Defaults to `"Remove"`. */
  dismissLabel?: string;
}

/** Chip renders a small dismissible tag for active filters and selections. */
const Chip = forwardRef<HTMLSpanElement, ChipProps>(
  (
    {
      className,
      tone = 'neutral',
      color,
      size = 'md',
      onDismiss,
      dismissLabel = 'Remove',
      children,
      style,
      title,
      ...rest
    },
    ref,
  ): ReactElement => (
    <span
      ref={ref}
      className={cx(
        styles.root,
        size === 'sm' && styles.sm,
        color != null && styles.custom,
        color == null && tone === 'success' && styles.success,
        color == null && tone === 'warning' && styles.warning,
        color == null && tone === 'danger' && styles.danger,
        color == null && tone === 'info' && styles.info,
        color == null && tone === 'accent' && styles.accent,
        className,
      )}
      style={color != null ? ({ ...style, '--chip-color': color } as CSSProperties) : style}
      title={title ?? (typeof children === 'string' ? children : undefined)}
      {...rest}
    >
      {color != null && <span aria-hidden className={styles.dot} />}
      <span className={styles.label}>{children}</span>
      {onDismiss != null && (
        <button
          type="button"
          aria-label={dismissLabel}
          onClick={onDismiss}
          className={styles.dismiss}
        >
          <X aria-hidden focusable={false} size="1em" />
        </button>
      )}
    </span>
  ),
);
Chip.displayName = 'Chip';

export default Chip;
