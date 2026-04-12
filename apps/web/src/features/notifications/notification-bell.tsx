/**
 * NotificationBell — top-bar icon button that shows the unread
 * notification count and toggles the notification dropdown.
 */

import Icon from '@nodate-flow/ui/icon';
import { Bell } from 'lucide-react';
import { type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import topBarStyles from '../../components/layout/top-bar.module.css';
import { useUnreadCountQuery } from './api';
import NotificationDropdown from './notification-dropdown';
import styles from './notifications.module.css';

export default function NotificationBell(): ReactElement {
  const { t } = useTranslation('notifications');
  const [open, setOpen] = useState(false);
  const { data: unreadCount } = useUnreadCountQuery();
  const count = unreadCount ?? 0;

  const handleToggle = (): void => {
    setOpen((prev) => !prev);
  };

  const handleClose = (): void => {
    setOpen(false);
  };

  return (
    <div className={styles.bellWrapper}>
      <button
        type="button"
        className={topBarStyles.iconButton}
        onClick={handleToggle}
        aria-label={count > 0 ? t('badge.unread', { count }) : t('view.title')}
      >
        <Icon icon={Bell} decorative />
        {count > 0 && (
          <span className={styles.badge} aria-hidden="true">
            {count > 99 ? '99+' : count}
          </span>
        )}
      </button>
      {open && (
        <Suspense fallback={null}>
          <NotificationDropdown onClose={handleClose} />
        </Suspense>
      )}
    </div>
  );
}
