/**
 * FormField — composes a label, optional description, optional error message,
 * and a control. Wires `htmlFor`, `aria-describedby`, and `aria-invalid` onto
 * the rendered control via a render-prop so callers keep full control of the
 * input element.
 */

import { forwardRef, type HTMLAttributes, type ReactElement, type ReactNode, useId } from 'react';
import { cx } from '../../lib/cx';
import styles from './form-field.module.css';

export interface FormFieldControlProps {
  id: string;
  'aria-describedby'?: string;
  'aria-invalid'?: boolean;
}

export interface FormFieldProps extends Omit<HTMLAttributes<HTMLDivElement>, 'children'> {
  /** Already-translated label text. */
  label: ReactNode;
  /** Optional already-translated description rendered below the control. */
  description?: ReactNode;
  /** Optional already-translated error message. Presence implies invalid. */
  error?: ReactNode;
  /** Whether to render the required indicator after the label. */
  required?: boolean;
  /** Render-prop receiving the wiring props to spread on the control. */
  children: (control: FormFieldControlProps) => ReactNode;
}

/** FormField wires label, description, and error state onto a child control. */
const FormField = forwardRef<HTMLDivElement, FormFieldProps>(
  ({ className, label, description, error, required, children, ...rest }, ref): ReactElement => {
    const inputId = useId();
    const descId = useId();
    const errorId = useId();

    const describedBy: string[] = [];
    if (description) describedBy.push(descId);
    if (error) describedBy.push(errorId);

    const control: FormFieldControlProps = {
      id: inputId,
      ...(describedBy.length > 0 ? { 'aria-describedby': describedBy.join(' ') } : {}),
      ...(error ? { 'aria-invalid': true } : {}),
    };

    return (
      <div ref={ref} className={cx(styles.root, className)} {...rest}>
        <label className={styles.label} htmlFor={inputId}>
          {label}
          {required ? (
            <span className={styles.required} aria-hidden="true">
              *
            </span>
          ) : null}
        </label>
        {description ? (
          <span id={descId} className={styles.description}>
            {description}
          </span>
        ) : null}
        {children(control)}
        {error ? (
          <span id={errorId} className={styles.error}>
            {error}
          </span>
        ) : null}
      </div>
    );
  },
);
FormField.displayName = 'FormField';

export default FormField;
