/**
 * TriageSuggestionRow — single AI triage suggestion card with score badge,
 * recommended action, reasoning, and the shared SuggestionActions row.
 */

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Card from '@nodate-flow/ui/primitives/card';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import type { Suggestion } from '../ai-suggestions/store';
import SuggestionActions from '../ai-suggestions/suggestion-actions';

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
  const { t } = useTranslation('ai-suggestions');
  const tone = scoreTone(suggestion.score);
  return (
    <Card style={{ padding: '0.875rem 1rem' }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: '0.875rem' }}>
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: '0.375rem',
            minInlineSize: 0,
            flex: 1,
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', flexWrap: 'wrap' }}>
            <Badge tone={tone}>
              {t('triage.score')}: {suggestion.score.toFixed(2)}
            </Badge>
            <span
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: '0.8125rem',
                color: 'var(--nf-color-fg-muted)',
              }}
            >
              {suggestion.recommendedAction}
            </span>
          </div>
          <p style={{ margin: 0, fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg)' }}>
            {suggestion.reasoning}
          </p>
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
          disabled={disabled}
        />
      </div>
    </Card>
  );
}
