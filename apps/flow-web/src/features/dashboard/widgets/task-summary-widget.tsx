/**
 * TaskSummaryWidget — this week's task counts from the weekly digest.
 */

import { Link } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useWeeklyDigestQuery } from '../../weekly-digest/api';
import { WidgetBoundary, WidgetMessage } from './widget-parts';
import styles from './widgets.module.css';

function TaskSummaryBody({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('dashboard');
  const { data } = useWeeklyDigestQuery(workspaceId);
  const { counts } = data;
  const overdue = data.overdueOpen?.length ?? 0;
  const total = counts.open + counts.review + counts.waiting + counts.done + counts.cancelled;

  if (total === 0) {
    return (
      <WidgetMessage
        title={t('widgets.task_summary.empty_title')}
        hint={t('widgets.task_summary.empty_hint')}
      />
    );
  }

  const figures: { key: string; label: string; value: number; emphasis?: boolean }[] = [
    { key: 'open', label: t('widgets.task_summary.open'), value: counts.open },
    { key: 'review', label: t('widgets.task_summary.review'), value: counts.review },
    { key: 'waiting', label: t('widgets.task_summary.waiting'), value: counts.waiting },
    { key: 'done', label: t('widgets.task_summary.done'), value: counts.done },
    {
      key: 'overdue',
      label: t('widgets.task_summary.overdue'),
      value: overdue,
      emphasis: overdue > 0,
    },
  ];

  return (
    <div className={styles.body}>
      <dl className={styles.figures}>
        {figures.map((figure) => (
          <div key={figure.key} className={styles.figure}>
            <dt className={styles.figureLabel}>{figure.label}</dt>
            <dd className={figure.emphasis ? styles.figureValueEmphasis : styles.figureValue}>
              {figure.value}
            </dd>
          </div>
        ))}
      </dl>
      <Link
        to="/workspaces/$id/projects"
        params={{ id: workspaceId }}
        className={styles.seeAllInline}
      >
        {t('widgets.see_all')}
      </Link>
    </div>
  );
}

export default function TaskSummaryWidget({ workspaceId }: { workspaceId: string }): ReactElement {
  return (
    <WidgetBoundary>
      <TaskSummaryBody workspaceId={workspaceId} />
    </WidgetBoundary>
  );
}
