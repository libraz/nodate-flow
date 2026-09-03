/**
 * RelationSuggestions — compact panel showing AI-detected relation
 * suggestions for a task. Designed to be embedded in the task detail
 * view near the dependencies section.
 *
 * Returns `null` when there are no pending suggestions so it has zero
 * visual footprint when inactive.
 */

import Icon from '@nodate-flow/ui/icon';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link } from '@tanstack/react-router';
import { Check, X } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import {
  type RelationSuggestion,
  type ResolveAction,
  useRelationSuggestionsForTask,
  useResolveSuggestion,
} from './api';
import styles from './relations.module.css';

const KIND_CLASS: Record<RelationSuggestion['suggestedKind'], string | undefined> = {
  blocks: styles.kindBlocks,
  relates: styles.kindRelates,
  duplicates: styles.kindDuplicates,
};

/** Static i18n key map — avoids dynamic `t(\`kind.\${x}\`)`. */
const KIND_I18N_KEY: Record<RelationSuggestion['suggestedKind'], string> = {
  blocks: 'kind.blocks',
  relates: 'kind.relates',
  duplicates: 'kind.duplicates',
};

interface RelationSuggestionsProps {
  taskId: string;
}

/**
 * RelationSuggestions renders pending AI-detected relation suggestions
 * for the given task. Renders nothing when there are no suggestions or
 * while loading.
 */
export default function RelationSuggestions({
  taskId,
}: RelationSuggestionsProps): ReactElement | null {
  const { t } = useTranslation('relations');
  const { data: suggestions, isLoading } = useRelationSuggestionsForTask(taskId);
  const resolve = useResolveSuggestion();

  if (isLoading || !suggestions || suggestions.length === 0) {
    return null;
  }

  const handleResolve = (suggestionId: string, action: ResolveAction): void => {
    resolve.mutate(
      { suggestionId, action, taskId },
      {
        onSuccess: () => {
          const toastKey = action === 'accept' ? 'toast.accepted' : 'toast.dismissed';
          toaster.show({ tone: 'success', message: t(toastKey) });
        },
        onError: (err) => {
          toaster.show({ tone: 'danger', message: formatApiError(err, t, 'toast.dismissed') });
        },
      },
    );
  };

  return (
    <section className={styles.section} aria-label={t('suggestions')}>
      <header className={styles.header}>
        <h3 className={styles.title}>{t('suggestions')}</h3>
        <span className={styles.count}>{suggestions.length}</span>
      </header>
      <ul className={styles.list}>
        {suggestions.map((suggestion) => (
          <SuggestionRow
            key={suggestion.id}
            suggestion={suggestion}
            taskId={taskId}
            onResolve={handleResolve}
          />
        ))}
      </ul>
    </section>
  );
}

function SuggestionRow({
  suggestion,
  taskId,
  onResolve,
}: {
  suggestion: RelationSuggestion;
  taskId: string;
  onResolve: (suggestionId: string, action: ResolveAction) => void;
}): ReactElement {
  const { t } = useTranslation('relations');

  // Show the "other" task — if current task is source, link to target and vice versa.
  const isSource = suggestion.sourceTaskId === taskId;
  const otherTaskId = isSource ? suggestion.targetTaskId : suggestion.sourceTaskId;
  const otherTaskTitle = isSource ? suggestion.targetTaskTitle : suggestion.sourceTaskTitle;

  const confidencePercent = Math.round(suggestion.confidence * 100);

  const handleAccept = (e: React.MouseEvent): void => {
    e.stopPropagation();
    onResolve(suggestion.id, 'accept');
  };

  const handleDismiss = (e: React.MouseEvent): void => {
    e.stopPropagation();
    onResolve(suggestion.id, 'dismiss');
  };

  return (
    <li className={styles.row}>
      <span className={`${styles.kindBadge} ${KIND_CLASS[suggestion.suggestedKind]}`}>
        {t(KIND_I18N_KEY[suggestion.suggestedKind])}
      </span>
      <Link to="/tasks/$taskId" params={{ taskId: otherTaskId }} className={styles.taskLink}>
        {otherTaskTitle}
      </Link>
      <span className={styles.confidence}>
        {t('confidence', { score: String(confidencePercent) })}
      </span>
      <div className={styles.actions}>
        <button
          type="button"
          className={`${styles.actionButton} ${styles.acceptButton} nf-focus-ring`}
          onClick={handleAccept}
          aria-label={t('action.accept')}
        >
          <Icon icon={Check} decorative />
        </button>
        <button
          type="button"
          className={`${styles.actionButton} ${styles.dismissButton} nf-focus-ring`}
          onClick={handleDismiss}
          aria-label={t('action.dismiss')}
        >
          <Icon icon={X} decorative />
        </button>
      </div>
    </li>
  );
}
