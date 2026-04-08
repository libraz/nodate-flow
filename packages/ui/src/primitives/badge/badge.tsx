/**
 * Badge — small inline tag with semantic tone variants.
 */

import { type HTMLAttributes, type ReactElement, type Ref, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './badge.module.css';

export type BadgeTone = 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'accent';

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  /** Semantic tone. Defaults to `"neutral"`. */
  tone?: BadgeTone;
}

function BadgeImpl(
  { className, tone = 'neutral', ...rest }: BadgeProps,
  ref: Ref<HTMLSpanElement>,
): ReactElement {
  return (
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
  );
}

const Badge = forwardRef<HTMLSpanElement, BadgeProps>(BadgeImpl);
Badge.displayName = 'Badge';

export default Badge;
