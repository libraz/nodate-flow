/**
 * Chip — small dismissible tag for active filters and selections.
 */

import { type HTMLAttributes, type ReactElement, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './chip.module.css';

/** Semantic tone for the Chip. */
export type ChipTone = 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'accent';

/** Props for the {@link Chip} component. */
export interface ChipProps extends HTMLAttributes<HTMLSpanElement> {
  /** Semantic tone. Defaults to `"neutral"`. */
  tone?: ChipTone;
  /** Called when the dismiss button is clicked. When omitted, no dismiss button is rendered. */
  onDismiss?: () => void;
  /** Accessible label for the dismiss button. Defaults to `"Remove"`. */
  dismissLabel?: string;
}

/** Chip renders a small dismissible tag for active filters and selections. */
const Chip = forwardRef<HTMLSpanElement, ChipProps>(
  (
    { className, tone = 'neutral', onDismiss, dismissLabel = 'Remove', children, ...rest },
    ref,
  ): ReactElement => (
    <span
      ref={ref}
      className={cx(
        styles.root,
        tone === 'success' && styles.success,
        tone === 'warning' && styles.warning,
        tone === 'danger' && styles.danger,
        tone === 'info' && styles.info,
        tone === 'accent' && styles.accent,
        className,
      )}
      {...rest}
    >
      {children}
      {onDismiss != null && (
        <button
          type="button"
          aria-label={dismissLabel}
          onClick={onDismiss}
          className={styles.dismiss}
        >
          {'\u00d7'}
        </button>
      )}
    </span>
  ),
);
Chip.displayName = 'Chip';

export default Chip;
