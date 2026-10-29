/**
 * SaveStatus — the inline note that sits beside a settings form's
 * primary action: "Unsaved changes" while the form is dirty, "Saved"
 * after a successful save.
 *
 * {@link useSaveStatus} owns the saved flag: a successful save sets it,
 * and the form becoming dirty again clears it, so the note never claims
 * the current edits are stored. Only the saved confirmation sits in the
 * live region; the dirty hint is visual and is not announced per edit.
 */

import { usePresence } from '@nodate-flow/ui/hooks/use-presence';
import Icon from '@nodate-flow/ui/icon';
import { Check } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './save-status.module.css';

export interface SaveStatusState {
  saved: boolean;
  markSaved: () => void;
}

export function useSaveStatus(dirty: boolean): SaveStatusState {
  const [saved, setSaved] = useState(false);
  // Derived-state update during render: an edit after a save retracts the note.
  if (dirty && saved) setSaved(false);
  return { saved, markSaved: () => setSaved(true) };
}

export interface SaveStatusProps {
  saved: boolean;
  /** Pass the form's dirty flag to show the unsaved-changes hint. */
  dirty?: boolean;
}

export default function SaveStatus({ saved, dirty = false }: SaveStatusProps): ReactElement {
  const { t } = useTranslation('settings');
  const presence = usePresence<HTMLSpanElement>(saved);
  return (
    <span className={styles.root}>
      {dirty && !saved ? <span className={styles.unsaved}>{t('save_status.unsaved')}</span> : null}
      <span role="status" className={styles.region}>
        {presence.mounted ? (
          <span ref={presence.ref} data-state={presence.state} className={styles.saved}>
            <Icon icon={Check} decorative className={styles.icon} />
            {t('save_status.saved')}
          </span>
        ) : null}
      </span>
    </span>
  );
}
