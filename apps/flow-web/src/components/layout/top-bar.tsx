import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import Avatar from '@nodate-flow/ui/primitives/avatar';
import Popover from '@nodate-flow/ui/primitives/popover';
import { useNavigate } from '@tanstack/react-router';

import { Bell, LogOut, Search, Settings } from 'lucide-react';
import { type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';
import AiCostMeter from '../../features/ai-providers/cost-meter';
import { authStore, selectUser, useAuth } from '../../features/auth/auth-store';
import NotificationBell from '../../features/notifications/notification-bell';
import { authSdk } from '../../lib/sdk';
import { clearActiveWorkspaceId } from '../../lib/use-current-workspace';
import CommandPalette from './command-palette';
import styles from './top-bar.module.css';
import TopBarBreadcrumb from './top-bar-breadcrumb';
import WorkspaceSwitcher from './workspace-switcher';

/** Two-letter initials from a display name (falls back to "?"). */
function initialsFrom(name: string | undefined): string {
  if (!name) return '?';
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return '?';
  const first = parts[0]?.[0] ?? '';
  const last = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : '';
  return (first + last).toUpperCase() || '?';
}

export default function TopBar(): ReactElement {
  const { t } = useTranslation('common');
  const navigate = useNavigate();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const user = useAuth(selectUser);
  const initials = initialsFrom(user?.displayName);
  const avatarUrl = user?.avatarUrl ?? null;
  const avatarLabel = t('topbar.user_menu.avatar_alt', { name: user?.displayName ?? '' });

  const handleLogout = async (): Promise<void> => {
    try {
      // Logout lives on auth-api; route through the auth-api SDK so the
      // request goes to the right base URL with the same bearer/cookie
      // plumbing as the rest of the auth surface.
      await authSdk.POST('/auth/logout', {});
    } catch {
      // Even on network failure, clear local state and bounce to /login.
    }
    authStore.getState().clearSession();
    // Drop the persisted active workspace so the next login does not
    // restore the previous user's last-visited workspace as a sidebar
    // shortcut.
    clearActiveWorkspaceId();
    void navigate({ to: '/login', replace: true });
  };

  const userMenuContent = (
    <div className={styles.userMenu}>
      {user ? (
        <div className={styles.userMenuHeader}>
          <span className={styles.userMenuName}>{user.displayName}</span>
          {user.email ? <span className={styles.userMenuEmail}>{user.email}</span> : null}
        </div>
      ) : null}
      {/* biome-ignore lint/a11y/noNoninteractiveElementToInteractiveRole: deliberate ARIA menu — the ul / li role="presentation" / button role="menuitem" structure is the standard menu pattern. */}
      <ul className={styles.userMenuList} role="menu">
        <li role="presentation">
          <button
            role="menuitem"
            type="button"
            className={cx(styles.userMenuItem, 'nf-focus-ring-inset')}
            onClick={() => {
              setUserMenuOpen(false);
              void navigate({ to: '/settings/profile' });
            }}
          >
            <Icon icon={Settings} decorative />
            <span>{t('topbar.user_menu.settings')}</span>
          </button>
        </li>
        <li role="presentation">
          <button
            role="menuitem"
            type="button"
            className={cx(styles.userMenuItem, 'nf-focus-ring-inset')}
            onClick={() => {
              setUserMenuOpen(false);
              void navigate({ to: '/settings/notifications' });
            }}
          >
            <Icon icon={Bell} decorative />
            <span>{t('topbar.notifications.label')}</span>
          </button>
        </li>
        <li aria-hidden="true" className={styles.userMenuDivider} />
        <li role="presentation">
          <button
            role="menuitem"
            type="button"
            className={cx(styles.userMenuItem, 'nf-focus-ring-inset')}
            onClick={() => {
              setUserMenuOpen(false);
              void handleLogout();
            }}
          >
            <Icon icon={LogOut} decorative />
            <span>{t('auth.logout')}</span>
          </button>
        </li>
      </ul>
    </div>
  );

  return (
    <>
      <div className={styles.topBarShell}>
        <header className={styles.topBar}>
          <div className={styles.left}>
            {/* Brand is rendered in the sidebar; the top bar only carries
                the workspace switcher to avoid a duplicate wordmark. */}
            <Suspense fallback={null}>
              <WorkspaceSwitcher />
            </Suspense>
          </div>
          <div className={styles.center}>
            <div className={styles.breadcrumbSlot}>
              <Suspense fallback={null}>
                <TopBarBreadcrumb />
              </Suspense>
            </div>
            <div className={styles.search}>
              <button
                type="button"
                className={cx(styles.searchButton, 'nf-focus-ring')}
                aria-label={t('topbar.search.placeholder')}
                data-search-trigger
                onClick={() => {
                  setPaletteOpen(true);
                }}
              >
                <Icon icon={Search} decorative />
                <span className={styles.searchLabel}>{t('topbar.search.placeholder')}</span>
                <span className={styles.searchButtonShortcut} aria-hidden="true">
                  Cmd+K
                </span>
              </button>
            </div>
          </div>
          <div className={styles.right}>
            <AiCostMeter />
            <div className={styles.inlineNotificationBell}>
              <Suspense fallback={null}>
                <NotificationBell />
              </Suspense>
            </div>
            <Popover
              open={userMenuOpen}
              onOpenChange={setUserMenuOpen}
              placement="bottom-end"
              content={userMenuContent}
            >
              <button
                type="button"
                className={cx(styles.avatarTrigger, 'nf-focus-ring')}
                aria-label={t('topbar.user_menu.open')}
                aria-haspopup="menu"
                title={user?.displayName ?? ''}
              >
                <Avatar
                  alt={avatarLabel}
                  initials={initials}
                  size="md"
                  {...(avatarUrl ? { src: avatarUrl } : {})}
                />
              </button>
            </Popover>
          </div>
        </header>
      </div>
      <CommandPalette
        open={paletteOpen}
        onClose={() => {
          setPaletteOpen(false);
        }}
      />
    </>
  );
}
