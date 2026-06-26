/**
 * Textarea — primitive multi-line text input.
 */

import { forwardRef, type ReactElement, type TextareaHTMLAttributes } from 'react';
import { cx } from '../../lib/cx';
import styles from './textarea.module.css';

export interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  /** Marks the textarea as invalid (mirrors `aria-invalid`). */
  invalid?: boolean;
  /**
   * Direction hint. Defaults to `'auto'` so the browser picks LTR / RTL per
   * paragraph from the first strong-directional character. Pass `'ltr'` /
   * `'rtl'` to force a direction.
   */
  dir?: 'ltr' | 'rtl' | 'auto';
}

/** Textarea renders a styled native `<textarea>` element. */
const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(
  (
    { className, invalid, dir = 'auto', 'aria-invalid': ariaInvalid, ...rest },
    ref,
  ): ReactElement => (
    <textarea
      ref={ref}
      dir={dir}
      aria-invalid={ariaInvalid ?? (invalid ? true : undefined)}
      className={cx(styles.root, invalid && styles.invalid, className)}
      {...rest}
    />
  ),
);
Textarea.displayName = 'Textarea';

export default Textarea;
