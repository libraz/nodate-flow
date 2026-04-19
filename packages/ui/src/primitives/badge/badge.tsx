/**
 * Badge — small inline tag with semantic tone variants.
 */

import { type HTMLAttributes, type ReactElement, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './badge.module.css';

export type BadgeTone = 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'accent';

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  /** Semantic tone. Defaults to `"neutral"`. */
  tone?: BadgeTone;
}

/** Badge renders a small inline tag with semantic tone variants. */
const Badge = forwardRef<HTMLSpanElement, BadgeProps>(
  ({ className, tone = 'neutral', ...rest }, ref): ReactElement => (
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
    />
  ),
);
Badge.displayName = 'Badge';

export default Badge;
