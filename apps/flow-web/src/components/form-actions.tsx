/**
 * FormActions — the footer row of a form: buttons end-aligned, wrapping
 * on narrow widths, with the submit-shortcut hint pinned to the start.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import type { ReactElement, ReactNode } from 'react';
import styles from './form-actions.module.css';
import SubmitHint from './submit-hint';

export interface FormActionsProps {
  /**
   * Show the shortcut hint. Pass `{ disabled: true }` while the form's
   * submit button is disabled so the hint dims with it.
   */
  hint?: boolean | { disabled?: boolean };
  className?: string | undefined;
  children: ReactNode;
}

export default function FormActions({
  hint = false,
  className,
  children,
}: FormActionsProps): ReactElement {
  return (
    <div className={cx(styles.root, className)}>
      {hint ? (
        <SubmitHint
          className={styles.hint}
          disabled={typeof hint === 'object' ? hint.disabled : undefined}
        />
      ) : null}
      {children}
    </div>
  );
}
