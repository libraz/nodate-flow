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
      <div
        style={{
          padding: 'var(--nf-space-4)',
          textAlign: 'center',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {t('view.empty')}
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
      {data.events.map((ev) => (
        <EventCard key={ev.id} event={ev} />
      ))}
    </div>
  );
}
