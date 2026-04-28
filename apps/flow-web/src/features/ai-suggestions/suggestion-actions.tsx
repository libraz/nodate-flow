/**
 * SuggestionActions — reusable 3-button action row for AI suggestions
 * (apply / dismiss / edit). Domain-agnostic; consumers wire the callbacks
 * to whatever mutation they need. This is the scaffold for all
 * suggestion UIs (inbox triage, project planning, etc).
 */

import Button from '@nodate-flow/ui/primitives/button';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './suggestion-actions.module.css';

export interface SuggestionActionsProps {
  onApply: () => void;
  onDismiss: () => void;
  onEdit: () => void;
  /** Disable all three buttons (e.g. while a mutation is in flight). */
  disabled?: boolean;
}

export default function SuggestionActions({
  onApply,
  onDismiss,
  onEdit,
  disabled = false,
}: SuggestionActionsProps): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  return (
    <div className={styles.row}>
      <Button type="button" variant="primary" size="sm" onClick={onApply} disabled={disabled}>
        {t('action.apply')}
      </Button>
      <Button type="button" variant="ghost" size="sm" onClick={onEdit} disabled={disabled}>
        {t('action.edit')}
      </Button>
      <Button type="button" variant="ghost" size="sm" onClick={onDismiss} disabled={disabled}>
        {t('action.dismiss')}
      </Button>
    </div>
  );
}
