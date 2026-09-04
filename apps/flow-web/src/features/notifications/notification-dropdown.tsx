/**
 * NotificationDropdown — dropdown panel showing the notification list
 * with mark-read, archive, and mark-all-read actions. Rendered inside
 * a Suspense boundary by NotificationBell.
 */

import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useNavigate } from '@tanstack/react-router';
import { Archive, Check } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import {
  type NotificationItem,
  useArchiveNotification,
  useMarkAllRead,
  useMarkNotificationRead,
  useNotificationsInfiniteQuery,
} from './api';
import { NOTIFICATION_EVENT_KEY } from './event-keys';
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

/**
 * Maps a notification's resource pointer to the in-app destination
 * route. Returns `null` when the notification is informational only
 * (no recognised resource), in which case the row click only marks the
 * row as read.
 */
function resourceHref(resourceType: string, resourceId: string | null): string | null {
  if (!resourceId) return null;
  switch (resourceType) {
    case 'task':
      return `/tasks/${resourceId}`;
    default:
      return null;
  }
}

/**
 * The title to show for a notification.
 *
 * `eventType` is the source of truth — it is NOT NULL on every row and
 * indexed, so rows written before this existed translate too. The stored
 * `title` is the fallback and only reaches the screen for an event type
 * added on the server before a key was added here: English, but never
 * blank, and never a raw key in front of a reader.
 */
function notificationTitle(item: NotificationItem, t: (key: string) => string): string {
  const key = NOTIFICATION_EVENT_KEY[item.eventType];
  return key === undefined ? item.title : t(key);
}

function NotificationRow({
  item,
  locale,
  onNavigate,
}: {
  item: NotificationItem;
  locale: string;
  onNavigate: (href: string) => void;
}): ReactElement {
  const { t } = useTranslation('notifications');
  const markRead = useMarkNotificationRead();
  const archive = useArchiveNotification();
  const isUnread = item.readAt === null;
  const href = resourceHref(item.resourceType, item.resourceId);

  /**
   * The mutation rolls the unread dot back on failure, which alone leaves the
   * reader guessing whether the click registered. The toast says what the API
   * refused.
   */
  const runMarkRead = async (): Promise<void> => {
    try {
      await markRead.mutateAsync(item.id);
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'toast.mark_read_failed'),
      });
    }
  };

  const handleMarkRead = (e: React.MouseEvent): void => {
    e.stopPropagation();
    if (isUnread) void runMarkRead();
  };

  const handleArchive = async (e: React.MouseEvent): Promise<void> => {
    e.stopPropagation();
    try {
      await archive.mutateAsync(item.id);
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'toast.archive_failed'),
      });
    }
  };

  const handleRowClick = (): void => {
    if (isUnread) void runMarkRead();
    if (href) onNavigate(href);
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
        <p className={styles.notifTitle}>{notificationTitle(item, t)}</p>
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
            className={cx(styles.actionButton, 'nf-focus-ring')}
            onClick={handleMarkRead}
            aria-label={t('action.mark_read')}
          >
            <Icon icon={Check} decorative />
          </button>
        )}
        <button
          type="button"
          className={cx(styles.actionButton, 'nf-focus-ring')}
          onClick={(e) => {
            void handleArchive(e);
          }}
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
  const { data, hasNextPage, isFetchingNextPage, fetchNextPage } = useNotificationsInfiniteQuery();
  // Flat-map cached pages into a single ordered list. TanStack threads
  // the cursor through `pageParam` so the order is stable across loads.
  const items = data.pages.flatMap((p) => p.notifications);
  const wsId = useCurrentWorkspaceId();
  const markAll = useMarkAllRead(wsId);
  const navigate = useNavigate();

  const handleNavigate = (href: string): void => {
    onClose();
    void navigate({ to: href });
  };

  const hasUnread = items.some((item) => item.readAt === null);

  const handleMarkAll = async (): Promise<void> => {
    try {
      await markAll.mutateAsync();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'toast.mark_all_read_failed'),
      });
    }
  };

  const handleLoadMore = (): void => {
    if (!hasNextPage || isFetchingNextPage) return;
    void fetchNextPage();
  };

  return (
    <>
      {/* Invisible backdrop to capture outside clicks */}
      <div className={styles.backdrop} onClick={onClose} aria-hidden="true" />
      <div id={id} className={styles.dropdown} role="dialog" aria-label={t('view.title')}>
        <div className={styles.dropdownHeader}>
          <h2 className={styles.dropdownTitle}>{t('view.title')}</h2>
          <button
            type="button"
            className={cx(styles.markAllButton, 'nf-focus-ring')}
            onClick={() => {
              void handleMarkAll();
            }}
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
              <NotificationRow
                key={item.id}
                item={item}
                locale={locale}
                onNavigate={handleNavigate}
              />
            ))}
          </ul>
        )}
        {hasNextPage ? (
          <div className={styles.loadMoreRow}>
            <button
              type="button"
              className={cx(styles.loadMoreButton, 'nf-focus-ring')}
              onClick={handleLoadMore}
              disabled={isFetchingNextPage}
              aria-busy={isFetchingNextPage}
            >
              {isFetchingNextPage ? t('view.loading_more') : t('view.load_more')}
            </button>
          </div>
        ) : null}
      </div>
    </>
  );
}
