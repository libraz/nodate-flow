/**
 * SubmitHint — muted keyboard-shortcut reminder shown beside a form's
 * primary submit button. Pair it with `onSubmitShortcut` on the form;
 * `FormActions` renders it in the right place.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { submitShortcutKeys } from '../lib/submit-shortcut';
import styles from './submit-hint.module.css';

export interface SubmitHintProps {
  /** Dim the hint while the submit button is disabled. */
  disabled?: boolean | undefined;
  className?: string | undefined;
}

export default function SubmitHint({ disabled, className }: SubmitHintProps): ReactElement {
  const { t } = useTranslation('common');
  return (
    <span className={cx(styles.hint, className)} data-disabled={disabled || undefined}>
      <span className={styles.keys}>
        {submitShortcutKeys().map((key) => (
          <kbd key={key} className={styles.kbd}>
            {key}
          </kbd>
        ))}
      </span>
      {t('common.submit_shortcut.hint')}
    </span>
  );
}
