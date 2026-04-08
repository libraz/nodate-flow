import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import { Link } from '@tanstack/react-router';
import {
  Briefcase,
  CalendarDays,
  ChevronsLeft,
  ChevronsRight,
  Inbox,
  type LucideIcon,
  Settings,
} from 'lucide-react';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './sidebar.module.css';

const STORAGE_KEY = 'nf.sidebar-collapsed';
const LEGACY_STORAGE_KEY = 'nf:sidebar-collapsed';

interface NavItem {
  key: 'inbox' | 'today' | 'workspaces' | 'settings';
  icon: LucideIcon;
  /** Destination route (TanStack Router path). */
  to: '/workspaces' | '/inbox' | '/today' | '/settings/profile';
}

const NAV_ITEMS: readonly NavItem[] = [
  { key: 'inbox', icon: Inbox, to: '/inbox' },
  { key: 'today', icon: CalendarDays, to: '/today' },
  { key: 'workspaces', icon: Briefcase, to: '/workspaces' },
  { key: 'settings', icon: Settings, to: '/settings/profile' },
];

function readInitialCollapsed(): boolean {
  if (typeof window === 'undefined') return false;
  try {
    const current = window.localStorage.getItem(STORAGE_KEY);
    if (current !== null) return current === '1';
    // Migrate from the legacy colon-separator key.
    const legacy = window.localStorage.getItem(LEGACY_STORAGE_KEY);
    if (legacy !== null) {
      window.localStorage.setItem(STORAGE_KEY, legacy);
      window.localStorage.removeItem(LEGACY_STORAGE_KEY);
      return legacy === '1';
    }
    return false;
  } catch {
    return false;
  }
}

export default function Sidebar(): ReactElement {
  const { t } = useTranslation('common');
  const [collapsed, setCollapsed] = useState<boolean>(() => readInitialCollapsed());

  useEffect(() => {
    try {
      window.localStorage.setItem(STORAGE_KEY, collapsed ? '1' : '0');
    } catch {
      /* ignore */
    }
  }, [collapsed]);

  const handleToggle = (): void => {
    setCollapsed((prev) => !prev);
  };

  const labelKeyFor = (
    key: NavItem['key'],
  ): 'nav.inbox' | 'nav.today' | 'nav.workspaces' | 'nav.settings' => {
    switch (key) {
      case 'inbox':
        return 'nav.inbox';
      case 'today':
        return 'nav.today';
      case 'workspaces':
        return 'nav.workspaces';
      case 'settings':
        return 'nav.settings';
    }
  };

  return (
    <aside
      className={cx(styles.sidebar, collapsed && styles.collapsed)}
      data-collapsed={collapsed || undefined}
    >
      <div className={styles.brand}>
        <span className={styles.brandLabel}>nodate-flow</span>
      </div>
      <nav className={styles.nav} aria-label={t('nav.primary')}>
        {NAV_ITEMS.map((item) => {
          const label = t(labelKeyFor(item.key));
          return (
            <Link
              key={item.key}
              to={item.to}
              className={styles.item}
              activeProps={{ className: cx(styles.item, styles.itemActive) }}
            >
              <Icon icon={item.icon} decorative />
              <span className={styles.label}>{label}</span>
            </Link>
          );
        })}
      </nav>
      <div className={styles.footer}>
        <button
          type="button"
          className={styles.toggle}
          onClick={handleToggle}
          aria-label={collapsed ? t('nav.expand') : t('nav.collapse')}
          aria-expanded={!collapsed}
        >
          <Icon icon={collapsed ? ChevronsRight : ChevronsLeft} decorative />
        </button>
      </div>
    </aside>
  );
}
