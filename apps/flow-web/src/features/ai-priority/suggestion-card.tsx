/**
 * SuggestionCard — single AI priority suggestion row.
 *
 * Renders a before/after view (current → suggested) with priority badges,
 * a five-dot confidence indicator, the reason text, and Apply / Dismiss
 * affordances. Keeps its own per-card busy state while the parent's
 * mutation is in flight; dismissal is delegated to the parent so it can
 * coordinate the optimistic fade-out and toast.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import { Link } from '@tanstack/react-router';
import { ArrowRight } from 'lucide-react';
import { type ReactElement, useId } from 'react';
import { useTranslation } from 'react-i18next';

import type { TaskPriority } from '../tasks/api';
import { PRIORITY_COLOR, PRIORITY_TONE } from '../tasks/constants';
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

export interface SuggestionCardProps {
  suggestion: PrioritySuggestion;
  /** True while either Apply or Dismiss is mid-flight for this row. */
  busy?: boolean;
  /** True once the row has been marked for fade-out (post-apply / dismiss). */
  exiting?: boolean;
  onApply: (suggestion: PrioritySuggestion) => void;
  onDismiss: (suggestion: PrioritySuggestion) => void;
}

/** Single card used by the priority suggestions list. */
export default function SuggestionCard(props: SuggestionCardProps): ReactElement {
  const { suggestion, busy, exiting, onApply, onDismiss } = props;
  const { t } = useTranslation('aiPriority');
  const reasonId = useId();

  const current = clampPriority(suggestion.currentPriority);
  const suggested = clampPriority(suggestion.suggestedPriority);
  const level = confidenceLevel(suggestion.confidence);
  const dots = confidenceDots(suggestion.confidence);
  const confidencePct = Math.round(Math.max(0, Math.min(1, suggestion.confidence)) * 100);

  return (
    <li className={cx(styles.card, exiting && styles.exiting)} data-testid="ai-priority-card">
      <div className={styles.body}>
        <div className={styles.titleRow}>
          <Link to="/tasks/$taskId" params={{ taskId: suggestion.taskId }} className={styles.title}>
            {suggestion.title}
          </Link>
        </div>

        <div className={styles.transition}>
          <div className={styles.transitionSide}>
            <span className={styles.transitionLabel}>{t('card.from')}</span>
            <Badge tone={PRIORITY_TONE[current]} className={styles.priorityBadge}>
              <span
                aria-hidden
                className={styles.priorityDot}
                style={{ background: PRIORITY_COLOR[current] }}
              />
              {t(priorityLabelKey(current))}
            </Badge>
          </div>
          <ArrowRight aria-hidden className={styles.arrow} size={16} />
          <div className={styles.transitionSide}>
            <span className={styles.transitionLabel}>{t('card.to')}</span>
            <Badge tone={PRIORITY_TONE[suggested]} className={styles.priorityBadge}>
              <span
                aria-hidden
                className={styles.priorityDot}
                style={{ background: PRIORITY_COLOR[suggested] }}
              />
              {t(priorityLabelKey(suggested))}
            </Badge>
          </div>
        </div>

        <div className={styles.confidenceRow}>
          <span className={styles.confidenceLabel}>{t('card.confidence')}</span>
          <span
            className={styles.dots}
            role="img"
            aria-label={t('card.confidenceAria', {
              level: t(confidenceLabelKey(level)),
              percent: confidencePct,
            })}
          >
            {[0, 1, 2, 3, 4].map((i) => (
              <span key={i} aria-hidden className={cx(styles.dot, i < dots && styles.dotFilled)} />
            ))}
          </span>
          <span className={styles.confidenceText}>{t(confidenceLabelKey(level))}</span>
        </div>

        {suggestion.reason ? (
          <p id={reasonId} className={styles.reason}>
            <span className={styles.reasonLabel}>{t('card.reason')}: </span>
            {suggestion.reason}
          </p>
        ) : null}
      </div>

      <div className={styles.actions}>
        <Button
          type="button"
          variant="primary"
          size="sm"
          onClick={() => onApply(suggestion)}
          disabled={busy || exiting}
          aria-label={t('card.applyAria', { title: suggestion.title })}
        >
          {t('card.apply')}
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => onDismiss(suggestion)}
          disabled={busy || exiting}
          aria-label={t('card.dismissAria', { title: suggestion.title })}
        >
          {t('card.dismiss')}
        </Button>
      </div>
    </li>
  );
}
