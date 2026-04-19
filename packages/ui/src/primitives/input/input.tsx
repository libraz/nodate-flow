/**
 * Input — primitive text input. Wraps a native `<input>` and adds
 * design-system styling plus an `invalid` style hook driven by `aria-invalid`.
 */

import { type InputHTMLAttributes, type ReactElement, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './input.module.css';

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  /** Marks the input as invalid (mirrors `aria-invalid`). */
  invalid?: boolean;
}

/** Input renders a styled native `<input>` element. */
const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ className, invalid, type, 'aria-invalid': ariaInvalid, ...rest }, ref): ReactElement => (
    <input
      ref={ref}
      type={type ?? 'text'}
      aria-invalid={ariaInvalid ?? (invalid ? true : undefined)}
      className={cx(styles.root, invalid && styles.invalid, className)}
      {...rest}
    />
  ),
);
Input.displayName = 'Input';

export default Input;
