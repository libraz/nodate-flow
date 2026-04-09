import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import { useQuery } from '@tanstack/react-query';
import { Link, useRouterState } from '@tanstack/react-router';
import {
  Briefcase,
  CalendarDays,
  CalendarRange,
  ChevronsLeft,
  ChevronsRight,
  FolderKanban,
  Inbox,
  type LucideIcon,
  Settings,
} from 'lucide-react';
import { type ReactElement, Suspense, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { projectsKeys, useProjectsQuery } from '../../features/projects/api';
import { sdk } from '../../lib/sdk';
import styles from './sidebar.module.css';

const STORAGE_KEY = 'nf.sidebar-collapsed';
const LEGACY_STORAGE_KEY = 'nf:sidebar-collapsed';

interface NavItem {
  key: 'inbox' | 'today' | 'calendar' | 'workspaces' | 'settings';
  icon: LucideIcon;
  /** Destination route (TanStack Router path). */
  to: '/workspaces' | '/inbox' | '/today' | '/calendar' | '/settings/profile';
}

const NAV_ITEMS: readonly NavItem[] = [
  { key: 'inbox', icon: Inbox, to: '/inbox' },
  { key: 'today', icon: CalendarDays, to: '/today' },
  { key: 'calendar', icon: CalendarRange, to: '/calendar' },
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

/**
 * Extracts the current workspace id from the URL. Handles both
 * `/workspaces/{id}/...` (direct) and `/projects/{pid}/...` (indirect,
 * resolved via a non-suspense project fetch). Returns `null` outside
 * workspace- or project-scoped routes, or while the project→workspace
 * lookup is still in flight.
 */
function useCurrentWorkspaceId(): string | null {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const wsMatch = useMemo(() => /^\/workspaces\/([^/]+)(?:\/|$)/.exec(pathname), [pathname]);
  const projectMatch = useMemo(() => /^\/projects\/([^/]+)(?:\/|$)/.exec(pathname), [pathname]);
  const projectId = projectMatch ? (projectMatch[1] ?? null) : null;

  // Resolve the workspace id from the project id when the user is on a
  // project-scoped route. Non-suspense so the sidebar never blocks.
  const projectQuery = useQuery({
    queryKey: projectId ? projectsKeys.detail(projectId) : ['projects', 'detail', 'none'],
    enabled: projectId !== null,
    staleTime: 60_000,
    queryFn: async (): Promise<{ workspaceId: string } | null> => {
      if (!projectId) return null;
      const { data, error } = await sdk.GET('/projects/{prjId}', {
        params: { path: { prjId: projectId } },
      });
      if (error || !data) return null;
      return { workspaceId: data.workspaceId };
    },
  });

  if (wsMatch) return wsMatch[1] ?? null;
  if (projectQuery.data) return projectQuery.data.workspaceId;
  return null;
}

/**
 * WorkspaceProjectsSection — renders a list of projects under the
 * currently-active workspace as Sidebar entries. Suspends on first
 * load; the outer Sidebar wraps this in a Suspense with a null
 * fallback so the rest of the nav renders immediately.
 */
function WorkspaceProjectsSection({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data: projects } = useProjectsQuery(workspaceId);
  const visible = projects.filter((p) => !p.isArchived);
  if (visible.length === 0) return <></>;
  return (
    <div className={styles.section}>
      <div className={styles.sectionLabel}>{t('nav.workspaceProjects')}</div>
      {visible.map((p) => (
        <Link
          key={p.id}
          to="/projects/$projectId"
          params={{ projectId: p.id }}
          className={cx(styles.item, styles.subItem)}
          activeProps={{ className: cx(styles.item, styles.subItem, styles.itemActive) }}
        >
          <Icon icon={FolderKanban} decorative />
          <span className={styles.label}>{p.name}</span>
        </Link>
      ))}
    </div>
  );
}

export default function Sidebar(): ReactElement {
  const { t } = useTranslation('common');
  const [collapsed, setCollapsed] = useState<boolean>(() => readInitialCollapsed());
  const currentWorkspaceId = useCurrentWorkspaceId();

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
  ): 'nav.inbox' | 'nav.today' | 'nav.calendar' | 'nav.workspaces' | 'nav.settings' => {
    switch (key) {
      case 'inbox':
        return 'nav.inbox';
      case 'today':
        return 'nav.today';
      case 'calendar':
        return 'nav.calendar';
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
          const node = (
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
          // Render the project tree directly beneath the Workspaces
          // nav entry when we are inside a workspace-scoped route.
          if (item.key === 'workspaces' && currentWorkspaceId && !collapsed) {
            return (
              <div key={item.key}>
                {node}
                <Suspense fallback={null}>
                  <WorkspaceProjectsSection workspaceId={currentWorkspaceId} />
                </Suspense>
              </div>
            );
          }
          return node;
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
