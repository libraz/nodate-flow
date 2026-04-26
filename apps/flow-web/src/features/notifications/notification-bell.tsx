/**
 * NotificationBell — top-bar icon button that shows the unread
 * notification count and toggles the notification dropdown.
 */

import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import { Bell } from 'lucide-react';
import { type MouseEvent, type ReactElement, Suspense, useId, useState } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import topBarStyles from '../../components/layout/top-bar.module.css';
import { useUnreadCountQuery } from './api';
import NotificationDropdown from './notification-dropdown';
import styles from './notifications.module.css';

function NotificationBellImpl(): ReactElement {
  const { t } = useTranslation('notifications');
  const [open, setOpen] = useState(false);
  const { data: unreadCount } = useUnreadCountQuery();
  const count = unreadCount ?? 0;
  const dropdownId = useId();

  const handleToggle = (event: MouseEvent<HTMLButtonElement>): void => {
    // Defensive stopPropagation: the dropdown's backdrop is a fixed
    // full-viewport <div> that calls onClose on click. If the backdrop
    // mounts during the same click cycle, the synthetic event could
    // bubble to it and immediately close the panel we just opened.
    event.stopPropagation();
    setOpen((prev) => !prev);
  };

  const handleClose = (): void => {
    setOpen(false);
  };

  return (
    <div className={styles.bellWrapper}>
      <button
        type="button"
        className={cx(topBarStyles.iconButton, 'nf-focus-ring')}
        onClick={handleToggle}
        aria-label={count > 0 ? t('badge.unread', { count }) : t('view.title')}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={dropdownId}
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
          <NotificationDropdown id={dropdownId} onClose={handleClose} />
        </Suspense>
      )}
    </div>
  );
}

/**
 * NotificationBell — default export wraps the real implementation in a
 * local ErrorBoundary. The bell (and the dropdown rendered inside it) is
 * decorative; if anything inside throws synchronously (a sibling hook
 * blows up, a query escalates past the per-query `throwOnError: false`
 * opt-out, etc.) the bell silently disappears instead of collapsing the
 * entire authenticated route to the root FatalFallback.
 */
export default function NotificationBell(): ReactElement {
  return (
    <ErrorBoundary fallback={null}>
      <NotificationBellImpl />
    </ErrorBoundary>
  );
}
