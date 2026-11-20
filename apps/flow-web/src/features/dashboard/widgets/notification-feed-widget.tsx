/**
 * NotificationFeedWidget — the signed-in user's latest notifications that
 * belong to this workspace.
 */

import { Bell } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { useNotificationsQuery } from '../../notifications/api';
import { NOTIFICATION_EVENT_KEY } from '../../notifications/event-keys';
import { formatShortTime, WIDGET_MAX_ITEMS, WidgetBoundary, WidgetMessage } from './widget-parts';
import styles from './widgets.module.css';

function NotificationFeedBody({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t, i18n } = useTranslation('dashboard');
  const { t: tNotif } = useTranslation('notifications');
  const locale = i18n.resolvedLanguage ?? 'en';
  const { data } = useNotificationsQuery();

  const items = data.filter((item) => item.workspaceId === workspaceId);

  if (items.length === 0) {
    return (
      <WidgetMessage
        title={t('widgets.notification_feed.empty_title')}
        hint={t('widgets.notification_feed.empty_hint')}
      />
    );
  }

  return (
    <div className={styles.body}>
      <ul className={styles.list}>
        {items.slice(0, WIDGET_MAX_ITEMS).map((item) => {
          const key = NOTIFICATION_EVENT_KEY[item.eventType];
          const unread = item.readAt === null;
          return (
            <li key={item.id} className={styles.item}>
              <Bell size={14} aria-hidden className={styles.itemIcon} />
              <div className={styles.itemText}>
                <span className={styles.itemPrimary}>
                  {key === undefined ? item.title : tNotif(key)}
                </span>
                <span className={styles.itemSecondary}>
                  {unread ? (
                    <>
                      <span className={styles.unreadMark}>
                        {t('widgets.notification_feed.unread')}
                      </span>
                      {' · '}
                    </>
                  ) : null}
                  {item.resourceTitle ? `${item.resourceTitle} · ` : null}
                  {formatShortTime(item.createdAt, locale)}
                </span>
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

export default function NotificationFeedWidget({
  workspaceId,
}: {
  workspaceId: string;
}): ReactElement {
  return (
    <WidgetBoundary>
      <NotificationFeedBody workspaceId={workspaceId} />
    </WidgetBoundary>
  );
}
