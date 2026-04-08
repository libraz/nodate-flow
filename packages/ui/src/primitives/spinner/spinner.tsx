/**
 * Spinner — indeterminate progress indicator. Requires an already-translated
 * `label` for assistive technology.
 */

import { type HTMLAttributes, type ReactElement, type Ref, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './spinner.module.css';

export type SpinnerSize = 'sm' | 'md' | 'lg';

export interface SpinnerProps extends Omit<HTMLAttributes<HTMLSpanElement>, 'role'> {
  /** Size scale. Defaults to `"md"`. */
  size?: SpinnerSize;
  /** Already-translated accessible label. */
  label: string;
}

function SpinnerImpl(
  { className, size = 'md', label, ...rest }: SpinnerProps,
  ref: Ref<HTMLSpanElement>,
): ReactElement {
  return (
    <span
      ref={ref}
      role="status"
      aria-live="polite"
      aria-label={label}
      className={cx(styles.root, size === 'sm' && styles.sm, size === 'lg' && styles.lg, className)}
      {...rest}
    />
  );
}

const Spinner = forwardRef<HTMLSpanElement, SpinnerProps>(SpinnerImpl);
Spinner.displayName = 'Spinner';

export default Spinner;
