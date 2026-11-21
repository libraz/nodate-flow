/**
 * CapsLockHint — warning shown under a field while CapsLock is on. It eases
 * open inside `Collapse`, so the control the reader is typing in never moves.
 * Link it with `aria-describedby` through {@link describedByWith}.
 */

import Collapse from '@nodate-flow/ui/primitives/collapse';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './caps-lock-hint.module.css';

export default function CapsLockHint({ open, id }: { open: boolean; id: string }): ReactElement {
  const { t } = useTranslation('auth');
  return (
    <Collapse open={open} className={styles.slot}>
      <span id={id} className={styles.hint}>
        {t('login.caps_lock_on')}
      </span>
    </Collapse>
  );
}

/** FormField's `aria-describedby` with the hint's id appended while it is shown. */
export function describedByWith(
  base: string | undefined,
  hintId: string,
  open: boolean,
): string | undefined {
  return open ? [base, hintId].filter(Boolean).join(' ') : base;
}
