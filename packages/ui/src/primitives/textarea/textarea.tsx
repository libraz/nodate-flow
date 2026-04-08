/**
 * Textarea — primitive multi-line text input.
 */

import { type ReactElement, type Ref, type TextareaHTMLAttributes, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './textarea.module.css';

export interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  /** Marks the textarea as invalid (mirrors `aria-invalid`). */
  invalid?: boolean;
}

function TextareaImpl(
  { className, invalid, 'aria-invalid': ariaInvalid, ...rest }: TextareaProps,
  ref: Ref<HTMLTextAreaElement>,
): ReactElement {
  return (
    <textarea
      ref={ref}
      aria-invalid={ariaInvalid ?? (invalid ? true : undefined)}
      className={cx(styles.root, invalid && styles.invalid, className)}
      {...rest}
    />
  );
}

const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(TextareaImpl);
Textarea.displayName = 'Textarea';

export default Textarea;
