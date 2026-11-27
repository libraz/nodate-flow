/**
 * SubmitHint — muted keyboard-shortcut reminder shown beside a form's
 * primary submit button. Pair it with `onSubmitShortcut` on the form.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { submitShortcutKeys } from '../lib/submit-shortcut';
import styles from './submit-hint.module.css';

export default function SubmitHint(): ReactElement {
  const { t } = useTranslation('common');
  return (
    <span className={styles.hint}>
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
