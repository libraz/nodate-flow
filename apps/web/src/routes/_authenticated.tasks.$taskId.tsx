/**
 * /tasks/$taskId — minimal task detail placeholder.
 *
 * F8 will replace this with the full detail panel (description, comments,
 * actors, dependencies, attachments, timeline). For F6 we only render the
 * title and derived state so navigation from the board/list views works.
 */

import Card from '@nodate-flow/ui/primitives/card';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import { useTaskQuery } from '../features/tasks/api';

function TaskDetailPanel({ id }: { id: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data: task } = useTaskQuery(id);
  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1rem',
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
      }}
    >
      <h1
        style={{
          fontFamily: 'var(--font-display)',
          fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
          margin: 0,
        }}
      >
        {task.title}
      </h1>
      <Card style={{ padding: '1.25rem' }}>
        <dl style={{ margin: 0, display: 'grid', gridTemplateColumns: '8rem 1fr', gap: '0.5rem' }}>
          <dt style={{ color: 'var(--color-muted)' }}>{t('tasks.columns.status')}</dt>
          <dd style={{ margin: 0 }}>{task.derivedState}</dd>
          <dt style={{ color: 'var(--color-muted)' }}>{t('tasks.columns.priority')}</dt>
          <dd style={{ margin: 0 }}>{task.priority}</dd>
          {task.dueOn ? (
            <>
              <dt style={{ color: 'var(--color-muted)' }}>{t('tasks.columns.due')}</dt>
              <dd style={{ margin: 0 }}>{task.dueOn}</dd>
            </>
          ) : null}
        </dl>
      </Card>
    </section>
  );
}

function TaskDetailRoute(): ReactElement {
  const { taskId } = Route.useParams();
  return (
    <Suspense
      fallback={
        <div style={{ padding: '2rem', display: 'flex', flexDirection: 'column', gap: '1rem' }}>
          <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
          <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <TaskDetailPanel id={taskId} />
    </Suspense>
  );
}

export const Route = createFileRoute('/_authenticated/tasks/$taskId')({
  component: TaskDetailRoute,
});
