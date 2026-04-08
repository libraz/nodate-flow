import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import { Link } from '@tanstack/react-router';
import {
  Briefcase,
  CalendarDays,
  ChevronsLeft,
  ChevronsRight,
  FolderKanban,
  Inbox,
  type LucideIcon,
  Settings,
} from 'lucide-react';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './sidebar.module.css';

const STORAGE_KEY = 'nf:sidebar-collapsed';

interface NavItem {
  key: 'inbox' | 'today' | 'projects' | 'workspaces' | 'settings';
  icon: LucideIcon;
  /** Whether the destination route exists yet. */
  enabled: boolean;
}

const NAV_ITEMS: readonly NavItem[] = [
  { key: 'inbox', icon: Inbox, enabled: true },
  { key: 'today', icon: CalendarDays, enabled: false },
  { key: 'projects', icon: FolderKanban, enabled: false },
  { key: 'workspaces', icon: Briefcase, enabled: false },
  { key: 'settings', icon: Settings, enabled: false },
];

function readInitialCollapsed(): boolean {
  if (typeof window === 'undefined') return false;
  try {
    return window.localStorage.getItem(STORAGE_KEY) === '1';
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
  ): 'nav.inbox' | 'nav.today' | 'nav.projects' | 'nav.workspaces' | 'nav.settings' => {
    switch (key) {
      case 'inbox':
        return 'nav.inbox';
      case 'today':
        return 'nav.today';
      case 'projects':
        return 'nav.projects';
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
          if (item.enabled) {
            return (
              <Link
                key={item.key}
                to="/"
                className={styles.item}
                activeProps={{ className: cx(styles.item, styles.itemActive) }}
              >
                <Icon icon={item.icon} decorative />
                <span className={styles.label}>{label}</span>
              </Link>
            );
          }
          return (
            <span
              key={item.key}
              className={cx(styles.item, styles.itemDisabled)}
              aria-disabled="true"
            >
              <Icon icon={item.icon} decorative />
              <span className={styles.label}>{label}</span>
            </span>
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
