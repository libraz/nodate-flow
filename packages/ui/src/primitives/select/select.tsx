/**
 * Select — primitive wrapper around a native `<select>` element.
 * Children are the `<option>` nodes supplied by the caller.
 */

import { type ReactElement, type SelectHTMLAttributes, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './select.module.css';

export interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  /** Marks the select as invalid (mirrors `aria-invalid`). */
  invalid?: boolean;
}

/** Select renders a styled native `<select>` element. */
const Select = forwardRef<HTMLSelectElement, SelectProps>(
  ({ className, invalid, 'aria-invalid': ariaInvalid, children, ...rest }, ref): ReactElement => (
    <select
      ref={ref}
      aria-invalid={ariaInvalid ?? (invalid ? true : undefined)}
      className={cx(styles.root, invalid && styles.invalid, className)}
      {...rest}
    >
      {children}
    </select>
  ),
);
Select.displayName = 'Select';

export default Select;
