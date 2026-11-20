/**
 * NotificationBell — top-bar icon button that shows the unread
 * notification count and toggles the notification dropdown.
 */

import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import Popover from '@nodate-flow/ui/primitives/popover';
import { Bell } from 'lucide-react';
import { type ReactElement, Suspense, useState } from 'react';
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
  const badgeText = count > 99 ? '99+' : String(count);

  const handleClose = (): void => {
    setOpen(false);
  };

  // Portalled through Popover: the top bar's backdrop-filter makes it a
  // stacking context, so an in-place panel painted under the page below.
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      placement="bottom-end"
      className={styles.dropdownPanel}
      ariaLabel={t('view.title')}
      content={
        <Suspense fallback={null}>
          <NotificationDropdown onClose={handleClose} />
        </Suspense>
      }
    >
      <button
        type="button"
        className={cx(topBarStyles.iconButton, styles.bellButton, 'nf-focus-ring')}
        aria-label={count > 0 ? t('badge.unread', { count }) : t('view.title')}
      >
        <Icon icon={Bell} decorative />
        {/* Keyed on the shown text so a count change replays the pop-in. */}
        {count > 0 && (
          <span key={badgeText} className={styles.badge} aria-hidden="true">
            {badgeText}
          </span>
        )}
      </button>
    </Popover>
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
