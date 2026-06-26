/**
 * Spinner — indeterminate progress indicator. Requires an already-translated
 * `label` for assistive technology.
 */

import { forwardRef, type HTMLAttributes, type ReactElement } from 'react';
import { cx } from '../../lib/cx';
import styles from './spinner.module.css';

export type SpinnerSize = 'sm' | 'md' | 'lg';

export interface SpinnerProps extends Omit<HTMLAttributes<HTMLSpanElement>, 'role'> {
  /** Size scale. Defaults to `"md"`. */
  size?: SpinnerSize;
  /** Already-translated accessible label. */
  label: string;
}

/** Spinner renders an indeterminate progress indicator. */
const Spinner = forwardRef<HTMLSpanElement, SpinnerProps>(
  ({ className, size = 'md', label, ...rest }, ref): ReactElement => (
    <span
      ref={ref}
      role="status"
      aria-live="polite"
      aria-label={label}
      className={cx(styles.root, size === 'sm' && styles.sm, size === 'lg' && styles.lg, className)}
      {...rest}
    />
  ),
);
Spinner.displayName = 'Spinner';

export default Spinner;
