/**
 * TaskMiniTimeline — compact, non-paginated, non-filtered timeline list
 * for embedding inside task detail panes.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useTaskTimelineQuery } from './api';
import EventCard from './event-card';

export interface TaskMiniTimelineProps {
  taskId: string;
}

export default function TaskMiniTimeline({ taskId }: TaskMiniTimelineProps): ReactElement {
  const { t } = useTranslation('timeline');
  const { data } = useTaskTimelineQuery(taskId, { limit: 10 });

  if (data.events.length === 0) {
    return (
      <div style={{ padding: '1rem', textAlign: 'center', color: 'var(--color-muted)' }}>
        {t('view.empty')}
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
      {data.events.map((ev) => (
        <EventCard key={ev.id} event={ev} />
      ))}
    </div>
  );
}
