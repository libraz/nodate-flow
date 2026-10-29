/**
 * DigestView — workspace weekly digest panel.
 *
 * Renders state counts, completed-this-week and overdue-open task
 * lists, and the server-rendered markdown body as a preformatted
 * block. Presentation only; the backend rule engine is the single
 * source of truth for what is reported.
 */

import { toaster } from '@nodate-flow/ui/primitives/toast';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import CopyButton from '../settings/copy-button';
import ui from '../settings/settings-ui.module.css';
import TruncatedText from '../settings/truncated-text';
import { useWeeklyDigestQuery, type WeeklyDigestCounts, type WeeklyDigestTask } from './api';
import styles from './digest-view.module.css';

function CountsRow({ counts }: { counts: WeeklyDigestCounts }): ReactElement {
  const { t } = useTranslation('settings');
  const items: Array<{ key: keyof WeeklyDigestCounts; label: string }> = [
    { key: 'open', label: t('weekly_digest.counts.open') },
    { key: 'waiting', label: t('weekly_digest.counts.waiting') },
    { key: 'review', label: t('weekly_digest.counts.review') },
    { key: 'done', label: t('weekly_digest.counts.done') },
    { key: 'cancelled', label: t('weekly_digest.counts.cancelled') },
  ];
  return (
    <dl className={styles.counts}>
      {items.map((item) => (
        <div key={item.key} className={styles.count}>
          <dt className={styles.countLabel}>{item.label}</dt>
          <dd className={styles.countValue}>{counts[item.key]}</dd>
        </div>
      ))}
    </dl>
  );
}

function TaskList({
  tasks,
  emptyLabel,
}: {
  tasks: WeeklyDigestTask[];
  emptyLabel: string;
}): ReactElement {
  if (tasks.length === 0) {
    return <p className={styles.empty}>{emptyLabel}</p>;
  }
  return (
    <ul className={styles.tasks}>
      {tasks.map((task) => (
        <li key={task.taskId} className={styles.task}>
          <TruncatedText text={task.title} />
          <span className={styles.taskDate}>{task.date}</span>
        </li>
      ))}
    </ul>
  );
}

export default function DigestView({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('settings');
  const { data } = useWeeklyDigestQuery(workspaceId);
  const completed = data.completedThisWeek ?? [];
  const overdue = data.overdueOpen ?? [];
  return (
    <div className={ui.page}>
      <header className={ui.header}>
        <div className={ui.headerText}>
          <h1 className={ui.title}>{t('weekly_digest.title')}</h1>
          <p className={ui.lede}>{t('weekly_digest.description')}</p>
        </div>
      </header>

      <CountsRow counts={data.counts} />

      <div className={styles.lists}>
        <section className={ui.section} aria-labelledby="digest-completed-title">
          <h2 id="digest-completed-title" className={ui.sectionTitle}>
            {t('weekly_digest.completed_this_week')}
          </h2>
          <TaskList tasks={completed} emptyLabel={t('weekly_digest.empty_completed')} />
        </section>

        <section className={ui.section} aria-labelledby="digest-overdue-title">
          <h2 id="digest-overdue-title" className={ui.sectionTitle}>
            {t('weekly_digest.overdue_open')}
          </h2>
          <TaskList tasks={overdue} emptyLabel={t('weekly_digest.empty_overdue')} />
        </section>
      </div>

      <section className={ui.section} aria-labelledby="digest-markdown-title">
        <div className={styles.markdownHeader}>
          <h2 id="digest-markdown-title" className={ui.sectionTitle}>
            {t('weekly_digest.markdown')}
          </h2>
          <CopyButton
            value={data.markdown}
            size="sm"
            label={t('weekly_digest.copy')}
            copiedLabel={t('weekly_digest.copied')}
            onError={() => {
              // error-toast-exempt: the clipboard write never reaches the API; its
              // DOMException message is untranslated UA text.
              toaster.show({ tone: 'danger', message: t('weekly_digest.copy_failed') });
            }}
          />
        </div>
        <pre className={styles.markdown}>{data.markdown}</pre>
      </section>
    </div>
  );
}
