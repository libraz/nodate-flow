/**
 * SuggestionCard — single AI priority suggestion row.
 *
 * Renders a before/after view (current → suggested) with priority badges,
 * a five-dot confidence indicator, the reason text, and Apply / Dismiss
 * affordances. Apply writes the task now; Dismiss only hides the row
 * locally, and each button's tooltip says so. The parent owns the list item, the busy state and the
 * exit, so it can coordinate the optimistic removal and toast.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { Link } from '@tanstack/react-router';
import { ArrowRight, Check, EyeOff } from 'lucide-react';
import { type ReactElement, useId } from 'react';
import { useTranslation } from 'react-i18next';

import type { TaskPriority } from '../tasks/api';
import { PRIORITY_TONE } from '../tasks/constants';
import type { PrioritySuggestion } from './api';
import styles from './suggestion-card.module.css';

/** Static i18n keys per priority bucket. */
function priorityLabelKey(p: TaskPriority): string {
  switch (p) {
    case 0:
      return 'priority.p0';
    case 1:
      return 'priority.p1';
    case 2:
      return 'priority.p2';
    case 3:
      return 'priority.p3';
    case 4:
      return 'priority.p4';
  }
}

/** Clamp an arbitrary integer to the legal {@link TaskPriority} range. */
function clampPriority(value: number): TaskPriority {
  if (value <= 0) return 0;
  if (value >= 4) return 4;
  return Math.round(value) as TaskPriority;
}

type ConfidenceLevel = 'low' | 'medium' | 'high';

/** Static i18n keys per confidence bucket. */
function confidenceLabelKey(l: ConfidenceLevel): string {
  switch (l) {
    case 'low':
      return 'card.confidenceLevel.low';
    case 'medium':
      return 'card.confidenceLevel.medium';
    case 'high':
      return 'card.confidenceLevel.high';
  }
}

/**
 * Bucket a 0..1 confidence score into low / medium / high. Thresholds match
 * the rest of the AI surface (`ai-suggestions.confidence.*`).
 */
function confidenceLevel(confidence: number): ConfidenceLevel {
  if (confidence >= 0.75) return 'high';
  if (confidence >= 0.5) return 'medium';
  return 'low';
}

/** Convert 0..1 confidence into a 1..5 dot count. */
function confidenceDots(confidence: number): number {
  const clamped = Math.max(0, Math.min(1, confidence));
  return Math.max(1, Math.round(clamped * 5));
}

/** Static i18n key per server reason code; unknown codes fall back to "steady". */
function reasonKey(code: string): string {
  switch (code) {
    case 'overdue':
      return 'reason_code.overdue';
    case 'due_by_tomorrow':
      return 'reason_code.due_by_tomorrow';
    case 'due_this_week':
      return 'reason_code.due_this_week';
    case 'due_this_fortnight':
      return 'reason_code.due_this_fortnight';
    case 'in_review':
      return 'reason_code.in_review';
    case 'many_dependents':
      return 'reason_code.many_dependents';
    case 'unassigned':
      return 'reason_code.unassigned';
    default:
      return 'reason_code.steady';
  }
}

export interface SuggestionCardProps {
  suggestion: PrioritySuggestion;
  /** True while either Apply or Dismiss is mid-flight for this row. */
  busy?: boolean;
  onApply: (suggestion: PrioritySuggestion) => void;
  onDismiss: (suggestion: PrioritySuggestion) => void;
}

/** Single card used by the priority suggestions list. */
export default function SuggestionCard(props: SuggestionCardProps): ReactElement {
  const { suggestion, busy, onApply, onDismiss } = props;
  const { t, i18n } = useTranslation('aiPriority');
  const reasonId = useId();
  const titleId = useId();

  const current = clampPriority(suggestion.currentPriority);
  const suggested = clampPriority(suggestion.suggestedPriority);
  const level = confidenceLevel(suggestion.confidence);
  const dots = confidenceDots(suggestion.confidence);
  const confidencePct = Math.round(Math.max(0, Math.min(1, suggestion.confidence)) * 100);

  const reasons = suggestion.reasons ?? [];
  const reasonText =
    reasons.length === 0
      ? t('reason_code.steady')
      : new Intl.ListFormat(i18n.language, { style: 'long', type: 'conjunction' }).format(
          reasons.map((code) => t(reasonKey(code), { count: suggestion.dependentCount })),
        );

  return (
    <article className={styles.card} data-testid="ai-priority-card" aria-labelledby={titleId}>
      <div className={styles.body}>
        <div className={styles.titleRow}>
          <Link
            id={titleId}
            to="/tasks/$taskId"
            params={{ taskId: suggestion.taskId }}
            className={styles.title}
          >
            {suggestion.title}
          </Link>
        </div>

        <div className={styles.transition}>
          <div className={styles.transitionSide}>
            <span className={styles.transitionLabel}>{t('card.from')}</span>
            <Badge tone={PRIORITY_TONE[current]} className={styles.priorityBadge}>
              {t(priorityLabelKey(current))}
            </Badge>
          </div>
          <ArrowRight aria-hidden className={styles.arrow} size={16} />
          <div className={styles.transitionSide}>
            <span className={styles.transitionLabel}>{t('card.to')}</span>
            <Badge tone={PRIORITY_TONE[suggested]} className={styles.priorityBadge}>
              {t(priorityLabelKey(suggested))}
            </Badge>
          </div>
        </div>

        <div className={styles.confidenceRow}>
          <span className={styles.confidenceLabel}>{t('card.confidence')}</span>
          <Tooltip content={t('card.confidenceHint')}>
            {/* Focusable so keyboard users reach the tooltip explaining the meter. */}
            <span
              className={cx(styles.dots, 'nf-focus-ring')}
              role="img"
              tabIndex={0}
              aria-label={t('card.confidenceAria', {
                level: t(confidenceLabelKey(level)),
                percent: confidencePct,
              })}
            >
              {[0, 1, 2, 3, 4].map((i) => (
                <span
                  key={i}
                  aria-hidden
                  className={cx(styles.dot, i < dots && styles.dotFilled)}
                />
              ))}
            </span>
          </Tooltip>
          <span className={styles.confidenceText}>{t(confidenceLabelKey(level))}</span>
        </div>

        <p id={reasonId} className={styles.reason}>
          <span className={styles.reasonLabel}>{t('card.reason')}: </span>
          {reasonText}
        </p>
      </div>

      <div className={styles.actions}>
        <Tooltip content={t('card.applyHint')}>
          <Button
            type="button"
            variant="primary"
            onClick={() => onApply(suggestion)}
            disabled={busy}
            aria-busy={busy || undefined}
            aria-label={t('card.applyAria', { title: suggestion.title })}
          >
            <Check size={14} aria-hidden />
            {t('card.apply')}
          </Button>
        </Tooltip>
        <Tooltip content={t('card.dismissHint')}>
          <Button
            type="button"
            variant="ghost"
            onClick={() => onDismiss(suggestion)}
            disabled={busy}
            aria-label={t('card.dismissAria', { title: suggestion.title })}
          >
            <EyeOff size={14} aria-hidden />
            {t('card.dismiss')}
          </Button>
        </Tooltip>
      </div>
    </article>
  );
}
