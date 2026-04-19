/**
 * Textarea — primitive multi-line text input.
 */

import { type ReactElement, type TextareaHTMLAttributes, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './textarea.module.css';

export interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  /** Marks the textarea as invalid (mirrors `aria-invalid`). */
  invalid?: boolean;
}

/** Textarea renders a styled native `<textarea>` element. */
const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(
  ({ className, invalid, 'aria-invalid': ariaInvalid, ...rest }, ref): ReactElement => (
    <textarea
      ref={ref}
      aria-invalid={ariaInvalid ?? (invalid ? true : undefined)}
      className={cx(styles.root, invalid && styles.invalid, className)}
      {...rest}
    />
  ),
);
Textarea.displayName = 'Textarea';

export default Textarea;
