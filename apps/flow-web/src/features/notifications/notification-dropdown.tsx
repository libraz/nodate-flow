/**
 * NotificationDropdown — dropdown panel showing the notification list
 * with mark-read, archive, and mark-all-read actions. Rendered inside
 * a Suspense boundary by NotificationBell.
 */

import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import { Archive, Check } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import {
  type NotificationItem,
  useArchiveNotification,
  useMarkAllRead,
  useMarkNotificationRead,
  useNotificationsQuery,
} from './api';
import styles from './notifications.module.css';

/** Relative time string from a unix-seconds timestamp. */
function formatRelative(unixSec: number, locale: string): string {
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
  const rawDiff = unixSec - Math.floor(Date.now() / 1000);
  const diffSec = rawDiff > 0 ? 0 : rawDiff;
  const abs = Math.abs(diffSec);
  if (abs < 60) return rtf.format(Math.round(diffSec), 'second');
  if (abs < 3600) return rtf.format(Math.round(diffSec / 60), 'minute');
  if (abs < 86_400) return rtf.format(Math.round(diffSec / 3600), 'hour');
  if (abs < 2_592_000) return rtf.format(Math.round(diffSec / 86_400), 'day');
  return rtf.format(Math.round(diffSec / 2_592_000), 'month');
}

interface NotificationDropdownProps {
  /** DOM id applied to the dialog, matched by the bell's aria-controls. */
  id: string;
  onClose: () => void;
}

function NotificationRow({
  item,
  locale,
}: {
  item: NotificationItem;
  locale: string;
}): ReactElement {
  const { t } = useTranslation('notifications');
  const markRead = useMarkNotificationRead();
  const archive = useArchiveNotification();
  const isUnread = item.readAt === null;

  const handleMarkRead = (e: React.MouseEvent): void => {
    e.stopPropagation();
    if (isUnread) markRead.mutate(item.id);
  };

  const handleArchive = (e: React.MouseEvent): void => {
    e.stopPropagation();
    archive.mutate(item.id);
  };

  const handleRowClick = (): void => {
    if (isUnread) markRead.mutate(item.id);
  };

  return (
    <li
      className={cx(styles.notifItem, isUnread && styles.unread)}
      onClick={handleRowClick}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          handleRowClick();
        }
      }}
    >
      <span className={isUnread ? styles.unreadDot : styles.readDot} aria-hidden="true" />
      <div className={styles.notifContent}>
        <p className={styles.notifTitle}>{item.title}</p>
        <div className={styles.notifMeta}>
          {item.actorDisplayName && (
            <span className={styles.notifActor}>{item.actorDisplayName}</span>
          )}
          <span className={styles.notifTime}>{formatRelative(item.createdAt, locale)}</span>
        </div>
      </div>
      <div className={styles.notifActions}>
        {isUnread && (
          <button
            type="button"
            className={styles.actionButton}
            onClick={handleMarkRead}
            aria-label={t('action.mark_read')}
          >
            <Icon icon={Check} decorative />
          </button>
        )}
        <button
          type="button"
          className={styles.actionButton}
          onClick={handleArchive}
          aria-label={t('action.archive')}
        >
          <Icon icon={Archive} decorative />
        </button>
      </div>
    </li>
  );
}

export default function NotificationDropdown({
  id,
  onClose,
}: NotificationDropdownProps): ReactElement {
  const { t, i18n } = useTranslation('notifications');
  const locale = i18n.resolvedLanguage ?? 'en';
  const { data: items } = useNotificationsQuery();
  const wsId = useCurrentWorkspaceId();
  const markAll = useMarkAllRead(wsId);

  const hasUnread = items.some((item) => item.readAt === null);

  const handleMarkAll = (): void => {
    markAll.mutate();
  };

  return (
    <>
      {/* Invisible backdrop to capture outside clicks */}
      {/* biome-ignore lint/a11y/useKeyWithClickEvents: backdrop dismiss */}
      <div className={styles.backdrop} onClick={onClose} aria-hidden="true" />
      <div id={id} className={styles.dropdown} role="dialog" aria-label={t('view.title')}>
        <div className={styles.dropdownHeader}>
          <h2 className={styles.dropdownTitle}>{t('view.title')}</h2>
          <button
            type="button"
            className={styles.markAllButton}
            onClick={handleMarkAll}
            disabled={!hasUnread || !wsId}
          >
            {t('view.mark_all_read')}
          </button>
        </div>
        {items.length === 0 ? (
          <div className={styles.empty}>{t('view.empty')}</div>
        ) : (
          <ul className={styles.notifList}>
            {items.map((item) => (
              <NotificationRow key={item.id} item={item} locale={locale} />
            ))}
          </ul>
        )}
      </div>
    </>
  );
}
