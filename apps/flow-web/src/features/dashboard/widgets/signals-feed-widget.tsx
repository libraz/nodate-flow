/**
 * SignalsFeedWidget — the latest `signal.*` events from the workspace timeline.
 */

import { Link } from '@tanstack/react-router';
import { Radio } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { TIMELINE_KINDS, useWorkspaceTimelineQuery } from '../../timeline/api';
import { formatShortTime, WIDGET_MAX_ITEMS, WidgetBoundary, WidgetMessage } from './widget-parts';
import styles from './widgets.module.css';

const SIGNAL_KINDS = TIMELINE_KINDS.filter((kind) => kind.startsWith('signal.'));
const SIGNAL_FILTERS = { kind: SIGNAL_KINDS, limit: 20 };

/** Static labels (timeline namespace); an unlisted kind falls back to the raw type. */
const signalKindKey = new Map<string, string>([['signal.attached', 'event_kind.signal_attached']]);

function SignalsFeedBody({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t, i18n } = useTranslation('dashboard');
  const { t: tTimeline } = useTranslation('timeline');
  const locale = i18n.resolvedLanguage ?? 'en';
  const { data } = useWorkspaceTimelineQuery(workspaceId, SIGNAL_FILTERS);

  const signals = data.events.filter((event) => event.type.startsWith('signal.'));

  if (signals.length === 0) {
    return (
      <WidgetMessage
        title={t('widgets.signals_feed.empty_title')}
        hint={t('widgets.signals_feed.empty_hint')}
      />
    );
  }

  return (
    <div className={styles.body}>
      <ul className={styles.list}>
        {signals.slice(0, WIDGET_MAX_ITEMS).map((event) => {
          const key = signalKindKey.get(event.type);
          const label = key === undefined ? event.type : tTimeline(key);
          return (
            <li key={event.id} className={styles.item}>
              <Radio size={14} aria-hidden className={styles.itemIcon} />
              <div className={styles.itemText}>
                {event.taskId ? (
                  <Link
                    to="/tasks/$taskId"
                    params={{ taskId: event.taskId }}
                    className={styles.itemPrimary}
                  >
                    {label}
                  </Link>
                ) : (
                  <span className={styles.itemPrimary}>{label}</span>
                )}
                <span className={styles.itemSecondary}>
                  {formatShortTime(event.occurredAt, locale)}
                </span>
              </div>
            </li>
          );
        })}
      </ul>
      <Link to="/workspaces/$id/timeline" params={{ id: workspaceId }} className={styles.seeAll}>
        {t('widgets.see_all')}
      </Link>
    </div>
  );
}

export default function SignalsFeedWidget({ workspaceId }: { workspaceId: string }): ReactElement {
  return (
    <WidgetBoundary>
      <SignalsFeedBody workspaceId={workspaceId} />
    </WidgetBoundary>
  );
}
