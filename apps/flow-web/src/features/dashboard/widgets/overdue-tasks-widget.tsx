/**
 * OverdueTasksWidget — reminders of kind `overdue`, most overdue first.
 */

import { Link } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { formatDateOnly } from '../../../lib/format';
import { useRemindersQuery } from '../../reminders/api';
import { WIDGET_MAX_ITEMS, WidgetError, WidgetMessage, WidgetSkeleton } from './widget-parts';
import styles from './widgets.module.css';

export default function OverdueTasksWidget({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t, i18n } = useTranslation('dashboard');
  const locale = i18n.resolvedLanguage ?? 'en';
  const query = useRemindersQuery(workspaceId);

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

  const overdue = query.data
    .filter((reminder) => reminder.kind === 'overdue')
    .sort((a, b) => a.daysUntilDue - b.daysUntilDue);

  if (overdue.length === 0) {
    return (
      <WidgetMessage
        title={t('widgets.overdue_tasks.empty_title')}
        hint={t('widgets.overdue_tasks.empty_hint')}
      />
    );
  }

  return (
    <div className={styles.body}>
      <ul className={styles.list}>
        {overdue.slice(0, WIDGET_MAX_ITEMS).map((reminder) => (
          <li key={reminder.taskId} className={styles.item}>
            <div className={styles.itemText}>
              <Link
                to="/tasks/$taskId"
                params={{ taskId: reminder.taskId }}
                className={styles.itemPrimary}
              >
                {reminder.title}
              </Link>
              <span className={styles.itemSecondary}>
                <span className={styles.itemEmphasis}>
                  {t('widgets.overdue_tasks.days_overdue', {
                    count: Math.abs(reminder.daysUntilDue),
                  })}
                </span>
                {' · '}
                {t('widgets.overdue_tasks.due', { date: formatDateOnly(reminder.dueOn, locale) })}
              </span>
            </div>
          </li>
        ))}
      </ul>
      <Link to="/workspaces/$id/reminders" params={{ id: workspaceId }} className={styles.seeAll}>
        {t('widgets.see_all')}
      </Link>
    </div>
  );
}
