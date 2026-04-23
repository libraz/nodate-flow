import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import Popover from '@nodate-flow/ui/primitives/popover';
import { useNavigate, useRouterState } from '@tanstack/react-router';

import { Bell, Globe, LogOut, Moon, Search, Sun } from 'lucide-react';
import { type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';
import CommandPalette from './command-palette';

import AiCostMeter from '../../features/ai-providers/cost-meter';
import { authStore, selectUser, useAuth } from '../../features/auth/auth-store';
import NotificationBell from '../../features/notifications/notification-bell';
import { useWorkspacesQuery } from '../../features/workspaces/api';
import { type SupportedLanguage, setLanguage } from '../../i18n';
import { apiBaseUrl } from '../../lib/sdk';
import { clearActiveWorkspaceId, useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import { type ConcreteTheme, concreteThemes, useTheme } from '../../providers/theme-provider';
import TopBarBreadcrumb from './top-bar-breadcrumb';
import styles from './top-bar.module.css';

function nextTheme(current: ConcreteTheme): ConcreteTheme {
  const idx = concreteThemes.indexOf(current);
  const next = concreteThemes[(idx + 1) % concreteThemes.length];
  return next ?? 'aurora-light';
}

/** Two-letter initials from a display name (falls back to "?"). */
function initialsFrom(name: string | undefined): string {
  if (!name) return '?';
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return '?';
  const first = parts[0]?.[0] ?? '';
  const last = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : '';
  return (first + last).toUpperCase() || '?';
}

/** Pages where switching workspace should NOT navigate away. */
const STAY_ON_PAGE_PREFIXES = ['/calendar', '/today', '/inbox', '/settings', '/pages'];

/**
 * WorkspaceSwitcher — native `<select>` dropdown in the topbar left slot.
 * Navigates to the chosen workspace. Highlights the current workspace when
 * the URL is under `/workspaces/{id}`. Suspends on first fetch; wrapped in
 * a Suspense with a tiny label fallback so the rest of the topbar renders
 * immediately.
 */
function WorkspaceSwitcher(): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const routeWsId = useCurrentWorkspaceId() ?? '';
  // Auto-select when there is exactly one workspace.
  const currentId = routeWsId || (workspaces.length === 1 ? (workspaces[0]?.id ?? '') : '');

  return (
    <select
      aria-label={t('topbar.workspace.switcher')}
      className={styles.workspaceSelect}
      value={currentId}
      onChange={(e) => {
        const id = e.target.value;
        if (!id) return;
        // On cross-workspace pages, stay on the current page.
        const stayOnPage = STAY_ON_PAGE_PREFIXES.some((p) => pathname.startsWith(p));
        if (stayOnPage) {
          // Just changing the select value is enough — the workspace
          // context propagates via useCurrentWorkspaceId and queries
          // will refetch. Navigate to the same page to force re-render.
          void navigate({ to: pathname as never });
        } else {
          void navigate({ to: '/workspaces/$id', params: { id } });
        }
      }}
    >
      <option value="" disabled>
        {t('topbar.workspace.none')}
      </option>
      {workspaces.map((w) => (
        <option key={w.id} value={w.id}>
          {w.name}
        </option>
      ))}
    </select>
  );
}

export default function TopBar(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { resolved, setPreference } = useTheme();
  const navigate = useNavigate();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const isDark = resolved === 'aurora-dark' || resolved === 'dotline-dark';
  const user = useAuth(selectUser);
  const initials = initialsFrom(user?.displayName);

  const handleThemeToggle = (): void => {
    setPreference(nextTheme(resolved));
  };

  const handleLanguageToggle = (): void => {
    const current = (i18n.resolvedLanguage ?? 'en') as SupportedLanguage;
    setLanguage(current === 'en' ? 'ja' : 'en');
  };

  const handleLogout = async (): Promise<void> => {
    try {
      await fetch(`${apiBaseUrl}/auth/logout`, {
        method: 'POST',
        credentials: 'include',
      });
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

  const currentLang = (i18n.resolvedLanguage ?? 'en').toUpperCase();

  const userMenuContent = (
    <div className={styles.userMenu}>
      {user ? (
        <div className={styles.userMenuHeader}>
          <span className={styles.userMenuName}>{user.displayName}</span>
          {user.email ? <span className={styles.userMenuEmail}>{user.email}</span> : null}
        </div>
      ) : null}
      <ul className={styles.userMenuList} role="menu">
        <li role="presentation">
          <button
            role="menuitem"
            type="button"
            className={styles.userMenuItem}
            onClick={() => {
              setUserMenuOpen(false);
              void navigate({ to: '/settings/notifications' });
            }}
          >
            <Icon icon={Bell} decorative />
            <span>{t('topbar.notifications.label')}</span>
          </button>
        </li>
        <li role="presentation">
          <button
            role="menuitem"
            type="button"
            className={styles.userMenuItem}
            onClick={() => {
              setUserMenuOpen(false);
              handleThemeToggle();
            }}
          >
            <Icon icon={isDark ? Moon : Sun} decorative />
            <span>{t('topbar.theme.toggle')}</span>
          </button>
        </li>
        <li role="presentation">
          <button
            role="menuitem"
            type="button"
            className={styles.userMenuItem}
            onClick={() => {
              setUserMenuOpen(false);
              handleLanguageToggle();
            }}
          >
            <Icon icon={Globe} decorative />
            <span>{t('topbar.language.toggle')}</span>
            <span className={styles.userMenuItemMeta} aria-hidden="true">
              {currentLang}
            </span>
          </button>
        </li>
        <li role="presentation">
          <button
            role="menuitem"
            type="button"
            className={styles.userMenuItem}
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
                className={styles.searchButton}
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
            <button
              type="button"
              className={cx(styles.iconButton, styles.inlineThemeToggle)}
              onClick={handleThemeToggle}
              aria-label={t('topbar.theme.toggle')}
            >
              <Icon icon={isDark ? Moon : Sun} decorative />
            </button>
            <button
              type="button"
              className={cx(styles.iconButton, styles.langToggle, styles.inlineLangToggle)}
              onClick={handleLanguageToggle}
              aria-label={t('topbar.language.toggle')}
            >
              {currentLang}
            </button>
            <button
              type="button"
              className={cx(styles.iconButton, styles.inlineSignOut)}
              onClick={() => {
                void handleLogout();
              }}
              aria-label={t('auth.logout')}
            >
              <Icon icon={LogOut} decorative />
            </button>
            <Popover
              open={userMenuOpen}
              onOpenChange={setUserMenuOpen}
              placement="bottom-end"
              content={userMenuContent}
            >
              <button
                type="button"
                className={styles.avatarTrigger}
                aria-label={t('topbar.user_menu.open')}
                aria-haspopup="menu"
                title={user?.displayName ?? ''}
              >
                {initials}
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
