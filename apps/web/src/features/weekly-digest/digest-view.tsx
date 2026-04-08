/**
 * DigestView — workspace weekly digest panel (2.AI-9 frontend).
 *
 * Renders state counts, completed-this-week and overdue-open task
 * lists, and the server-rendered markdown body as a preformatted
 * block. Presentation only; the backend rule engine is the single
 * source of truth for what is reported.
 */

import Card from '@nodate-flow/ui/primitives/card';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { type WeeklyDigestCounts, type WeeklyDigestTask, useWeeklyDigestQuery } from './api';

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
    <ul
      style={{
        listStyle: 'none',
        margin: 0,
        padding: 0,
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fit, minmax(7rem, 1fr))',
        gap: '0.5rem',
      }}
    >
      {items.map((item) => (
        <li key={item.key}>
          <Card style={{ padding: '0.625rem 0.75rem' }}>
            <div style={{ fontSize: '0.75rem', color: 'var(--color-muted)' }}>{item.label}</div>
            <div style={{ fontSize: '1.25rem', fontWeight: 600 }}>{counts[item.key]}</div>
          </Card>
        </li>
      ))}
    </ul>
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
    return (
      <p style={{ margin: 0, color: 'var(--color-muted)', fontSize: '0.8125rem' }}>{emptyLabel}</p>
    );
  }
  return (
    <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.25rem' }}>
      {tasks.map((task) => (
        <li
          key={task.taskId}
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            gap: '1rem',
            fontSize: '0.8125rem',
          }}
        >
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {task.title}
          </span>
          <span style={{ color: 'var(--color-muted)', fontFamily: 'var(--font-mono)' }}>
            {task.date}
          </span>
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
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
        <h1 style={{ margin: 0, fontSize: '1.5rem' }}>{t('weekly_digest.title')}</h1>
        <p style={{ margin: 0, color: 'var(--color-muted)', fontSize: '0.8125rem' }}>
          {t('weekly_digest.description')}
        </p>
      </header>

      <CountsRow counts={data.counts} />

      <section style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h2 style={{ margin: 0, fontSize: '0.9375rem' }}>
          {t('weekly_digest.completed_this_week')}
        </h2>
        <TaskList tasks={completed} emptyLabel={t('weekly_digest.empty_completed')} />
      </section>

      <section style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h2 style={{ margin: 0, fontSize: '0.9375rem' }}>{t('weekly_digest.overdue_open')}</h2>
        <TaskList tasks={overdue} emptyLabel={t('weekly_digest.empty_overdue')} />
      </section>

      <section style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h2 style={{ margin: 0, fontSize: '0.9375rem' }}>{t('weekly_digest.markdown')}</h2>
        <Card style={{ padding: '0.875rem 1rem' }}>
          <pre
            style={{
              margin: 0,
              fontFamily: 'var(--font-mono)',
              fontSize: '0.75rem',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-word',
            }}
          >
            {data.markdown}
          </pre>
        </Card>
      </section>
    </div>
  );
}
