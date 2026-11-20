/**
 * SuggestionActions — reusable 3-button action row for AI suggestions
 * (apply / dismiss / edit). Domain-agnostic; consumers wire the callbacks
 * to whatever mutation they need. This is the scaffold for all
 * suggestion UIs (inbox triage, project planning, etc).
 */

import Button from '@nodate-flow/ui/primitives/button';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { AiSparkle } from './ai-consequence';
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
      {/* Apply changes data with no further confirmation, so it carries the filled sparkle. */}
      <Tooltip content={t('action.apply_hint')}>
        <Button type="button" variant="primary" size="sm" onClick={onApply} disabled={disabled}>
          <AiSparkle kind="applies" />
          {t('action.apply')}
        </Button>
      </Tooltip>
      <Button type="button" variant="ghost" size="sm" onClick={onEdit} disabled={disabled}>
        {t('action.edit')}
      </Button>
      <Button type="button" variant="ghost" size="sm" onClick={onDismiss} disabled={disabled}>
        {t('action.dismiss')}
      </Button>
    </div>
  );
}
