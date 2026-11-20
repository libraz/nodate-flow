/**
 * NotificationDropdown — body of the notification panel: the list with
 * mark-read, archive, and mark-all-read actions. NotificationBell hosts it
 * in a Popover (which owns the dialog role, Escape, outside click and focus
 * return) inside a Suspense boundary.
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { useNavigate } from '@tanstack/react-router';
import { Archive, BellOff, Check, CircleHelp } from 'lucide-react';
import { type ReactElement, useId, useMemo, useState } from 'react';
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
  const diffSec = Math.min(rawDiff, 0);
  const abs = Math.abs(diffSec);
  if (abs < 60) return rtf.format(Math.round(diffSec), 'second');
  if (abs < 3600) return rtf.format(Math.round(diffSec / 60), 'minute');
  if (abs < 86_400) return rtf.format(Math.round(diffSec / 3600), 'hour');
  if (abs < 2_592_000) return rtf.format(Math.round(diffSec / 86_400), 'day');
  return rtf.format(Math.round(diffSec / 2_592_000), 'month');
}

interface NotificationDropdownProps {
  /** Closes the hosting popover, e.g. before navigating away. */
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
    <div
      className={cx(styles.notifItem, isUnread && styles.unread, 'nf-focus-ring-inset')}
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
        {item.resourceTitle && (
          <p className={styles.notifResource} title={item.resourceTitle}>
            {item.resourceTitle}
          </p>
        )}
        <div className={styles.notifMeta}>
          {item.actorDisplayName && (
            <span className={styles.notifActor}>{item.actorDisplayName}</span>
          )}
          <span className={styles.notifTime}>{formatRelative(item.createdAt, locale)}</span>
        </div>
      </div>
      <div className={styles.notifActions}>
        {isUnread && (
          <Tooltip content={t('action.mark_read')}>
            <button
              type="button"
              className={cx(styles.actionButton, 'nf-focus-ring')}
              onClick={handleMarkRead}
              aria-label={t('action.mark_read')}
            >
              <Icon icon={Check} decorative />
            </button>
          </Tooltip>
        )}
        <Tooltip content={t('action.archive')}>
          <button
            type="button"
            className={cx(styles.actionButton, 'nf-focus-ring')}
            onClick={(e) => {
              void handleArchive(e);
            }}
            aria-label={t('action.archive')}
            disabled={archive.isPending}
            aria-busy={archive.isPending || undefined}
          >
            <Icon icon={Archive} decorative />
          </button>
        </Tooltip>
      </div>
    </div>
  );
}

/** The `help:notifications` topic, shown in place under the panel header. */
function NotificationsHelp({ id }: { id: string }): ReactElement {
  const { t } = useTranslation('help');
  const rawTips = t('notifications.tips', { returnObjects: true, defaultValue: {} });
  const tips: string[] =
    typeof rawTips === 'object' && rawTips !== null && !Array.isArray(rawTips)
      ? Object.values(rawTips).filter((v): v is string => typeof v === 'string')
      : [];
  return (
    <section id={id} className={styles.help} aria-label={t('notifications.title')}>
      <p className={styles.helpSummary}>{t('notifications.summary')}</p>
      {tips.length > 0 ? (
        <ul className={styles.helpTips}>
          {tips.map((tip) => (
            <li key={tip}>{tip}</li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}

export default function NotificationDropdown({ onClose }: NotificationDropdownProps): ReactElement {
  const { t, i18n } = useTranslation('notifications');
  const locale = i18n.resolvedLanguage ?? 'en';
  const { data, hasNextPage, isFetchingNextPage, fetchNextPage } = useNotificationsInfiniteQuery();
  // Flat-map cached pages into a single ordered list. TanStack threads
  // the cursor through `pageParam` so the order is stable across loads.
  // Memoised because the presence list below diffs on array identity.
  const items = useMemo(() => data.pages.flatMap((p) => p.notifications), [data.pages]);
  const { entries, onExited } = usePresenceList(items, (item) => item.id);
  const stagger = useFirstLoadStagger();
  const wsId = useCurrentWorkspaceId();
  const markAll = useMarkAllRead(wsId);
  const navigate = useNavigate();
  // Inline rather than the PageHelp popover: a popover nested in this one
  // would count as an outside press and close the panel it explains.
  const [helpOpen, setHelpOpen] = useState(false);
  const helpId = useId();

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
    <div className={styles.dropdownBody}>
      <div className={styles.dropdownHeader}>
        <div className={styles.dropdownTitleRow}>
          <h2 className={styles.dropdownTitle}>{t('view.title')}</h2>
          <Tooltip content={t('common:help.trigger')}>
            <button
              type="button"
              className={cx(styles.helpToggle, 'nf-focus-ring')}
              aria-label={t('common:help.trigger')}
              aria-expanded={helpOpen}
              aria-controls={helpId}
              onClick={() => {
                setHelpOpen((v) => !v);
              }}
            >
              <Icon icon={CircleHelp} decorative />
            </button>
          </Tooltip>
        </div>
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
      {helpOpen ? <NotificationsHelp id={helpId} /> : null}
      {entries.length === 0 ? (
        <div className={styles.empty}>
          <Icon icon={BellOff} decorative />
          <span>{t('view.empty')}</span>
        </div>
      ) : (
        <ul className={styles.notifList}>
          {entries.map((entry, index) => (
            <PresenceItem
              key={entry.key}
              as="li"
              present={entry.present}
              onExited={() => onExited(entry.key)}
              className={motion.enter}
              style={stagger(index)}
            >
              <NotificationRow item={entry.item} locale={locale} onNavigate={handleNavigate} />
            </PresenceItem>
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
  );
}
