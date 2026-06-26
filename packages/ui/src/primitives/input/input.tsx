/**
 * Input — primitive text input. Wraps a native `<input>` and adds
 * design-system styling plus an `invalid` style hook driven by `aria-invalid`.
 */

import { forwardRef, type InputHTMLAttributes, type ReactElement } from 'react';
import { cx } from '../../lib/cx';
import styles from './input.module.css';

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  /** Marks the input as invalid (mirrors `aria-invalid`). */
  invalid?: boolean;
  /**
   * Direction hint for the input. Defaults to `'auto'` so the browser picks
   * LTR / RTL from the first strong-directional character in the value, which
   * is the right behaviour for free-form user text in mixed-direction locales.
   * Pass `'ltr'` / `'rtl'` to force a direction (e.g. an email field that
   * should always render LTR even when the surrounding UI is Arabic).
   */
  dir?: 'ltr' | 'rtl' | 'auto';
}

/** Input renders a styled native `<input>` element. */
const Input = forwardRef<HTMLInputElement, InputProps>(
  (
    { className, invalid, type, dir = 'auto', 'aria-invalid': ariaInvalid, ...rest },
    ref,
  ): ReactElement => (
    <input
      ref={ref}
      type={type ?? 'text'}
      dir={dir}
      aria-invalid={ariaInvalid ?? (invalid ? true : undefined)}
      className={cx(styles.root, invalid && styles.invalid, className)}
      {...rest}
    />
  ),
);
Input.displayName = 'Input';

export default Input;
