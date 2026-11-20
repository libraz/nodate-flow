/**
 * AiSuggestionsWidget — pending AI suggestions. These only propose; nothing
 * changes until one is applied from the inbox.
 */

import { Link } from '@tanstack/react-router';
import { Sparkles } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useAiSuggestionsQuery } from '../../ai-suggestions/api';
import { WIDGET_MAX_ITEMS, WidgetError, WidgetMessage, WidgetSkeleton } from './widget-parts';
import styles from './widgets.module.css';

export default function AiSuggestionsWidget({
  workspaceId,
}: {
  workspaceId: string;
}): ReactElement {
  const { t } = useTranslation('dashboard');
  const { t: tAi } = useTranslation('ai-suggestions');
  const query = useAiSuggestionsQuery(workspaceId);

  // Static labels; an unlisted action falls back to the raw value.
  const actionLabels = new Map<string, string>([
    ['open', tAi('edit.action.open')],
    ['snooze', tAi('edit.action.snooze')],
    ['archive', tAi('edit.action.archive')],
  ]);

  if (query.isPending) return <WidgetSkeleton />;
  if (query.isError) {
    return (
      <WidgetError
        onRetry={() => {
          void query.refetch();
        }}
      />
    );
  }

  const suggestions = [...query.data].sort((a, b) => b.score - a.score);

  if (suggestions.length === 0) {
    return (
      <WidgetMessage
        title={t('widgets.ai_suggestions.empty_title')}
        hint={t('widgets.ai_suggestions.empty_hint')}
      />
    );
  }

  return (
    <div className={styles.body}>
      <ul className={styles.list}>
        {suggestions.slice(0, WIDGET_MAX_ITEMS).map((suggestion) => (
          <li key={suggestion.proposalId} className={styles.item}>
            <Sparkles size={14} aria-hidden className={styles.itemIcon} />
            <div className={styles.itemText}>
              <span className={`${styles.itemPrimary} ${styles.itemClamp}`}>
                {suggestion.reasoning}
              </span>
              <span className={styles.itemSecondary}>
                {tAi('consequence.suggestion')}
                {' · '}
                {actionLabels.get(suggestion.recommendedAction) ?? suggestion.recommendedAction}
              </span>
            </div>
          </li>
        ))}
      </ul>
      <Link to="/inbox" className={styles.seeAll}>
        {t('widgets.see_all')}
      </Link>
    </div>
  );
}
