/**
 * Input — primitive text input. Wraps a native `<input>` and adds
 * design-system styling plus an `invalid` style hook driven by `aria-invalid`.
 */

import { type InputHTMLAttributes, type ReactElement, type Ref, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './input.module.css';

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  /** Marks the input as invalid (mirrors `aria-invalid`). */
  invalid?: boolean;
}

function InputImpl(
  { className, invalid, type, 'aria-invalid': ariaInvalid, ...rest }: InputProps,
  ref: Ref<HTMLInputElement>,
): ReactElement {
  return (
    <input
      ref={ref}
      type={type ?? 'text'}
      aria-invalid={ariaInvalid ?? (invalid ? true : undefined)}
      className={cx(styles.root, invalid && styles.invalid, className)}
      {...rest}
    />
  );
}

const Input = forwardRef<HTMLInputElement, InputProps>(InputImpl);
Input.displayName = 'Input';

export default Input;
