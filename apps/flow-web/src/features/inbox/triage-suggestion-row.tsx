/**
 * TriageSuggestionRow — single AI triage suggestion card with score badge,
 * recommended action, reasoning, and the shared SuggestionActions row.
 */

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Card from '@nodate-flow/ui/primitives/card';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { recommendedActionLabel } from '../ai-suggestions/recommended-action-label';
import type { Suggestion } from '../ai-suggestions/store';
import SuggestionActions from '../ai-suggestions/suggestion-actions';
import styles from './triage-suggestion-row.module.css';

export interface TriageSuggestionRowProps {
  suggestion: Suggestion;
  onApply: (suggestion: Suggestion) => void;
  onDismiss: (suggestion: Suggestion) => void;
  onEdit: (suggestion: Suggestion) => void;
  disabled?: boolean;
}

function scoreTone(score: number): BadgeTone {
  if (score >= 0.8) return 'danger';
  if (score >= 0.5) return 'warning';
  return 'neutral';
}

export default function TriageSuggestionRow({
  suggestion,
  onApply,
  onDismiss,
  onEdit,
  disabled = false,
}: TriageSuggestionRowProps): ReactElement {
  const { t, i18n } = useTranslation('ai-suggestions');
  const tone = scoreTone(suggestion.score);
  const needsRefresh = suggestion.sourceVersionStatus !== 'captured' || !suggestion.proposalId;
  const score = new Intl.NumberFormat(i18n.resolvedLanguage ?? 'en', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(suggestion.score);
  return (
    <Card className={styles.card}>
      <div className={styles.inner}>
        <div className={styles.body}>
          <div className={styles.meta}>
            <Badge tone={tone} className={styles.score}>
              {t('triage.score')}: {score}
            </Badge>
            <span className={styles.action}>
              {recommendedActionLabel(suggestion.recommendedAction, t)}
            </span>
          </div>
          <p className={styles.reasoning}>{suggestion.reasoning}</p>
          {needsRefresh ? (
            <p role="note" className={styles.note}>
              {t('triage.legacy_notice')}
            </p>
          ) : null}
        </div>
        <SuggestionActions
          onApply={() => {
            onApply(suggestion);
          }}
          onDismiss={() => {
            onDismiss(suggestion);
          }}
          onEdit={() => {
            onEdit(suggestion);
          }}
          disabled={disabled || needsRefresh}
        />
      </div>
    </Card>
  );
}
