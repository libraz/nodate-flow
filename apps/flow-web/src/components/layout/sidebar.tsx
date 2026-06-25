import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { BP } from '@nodate-flow/ui/tokens/breakpoints';
import { Link, useRouterState } from '@tanstack/react-router';
import {
  Activity,
  Briefcase,
  CalendarDays,
  CalendarRange,
  CheckSquare,
  ChevronsLeft,
  ChevronsRight,
  Eye,
  FileText,
  FolderKanban,
  Inbox,
  ListOrdered,
  type LucideIcon,
  Menu,
  Settings,
  Timer,
  X,
} from 'lucide-react';
import { type ReactElement, Suspense, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type Favorite, useFavoritesQuery } from '../../features/favorites/api';
import { useProjectsQuery } from '../../features/projects/api';
import { useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import styles from './sidebar.module.css';
import WorkspaceSwitcher from './workspace-switcher';

const STORAGE_KEY = 'nf.sidebar-collapsed';
const LEGACY_STORAGE_KEY = 'nf:sidebar-collapsed';

interface NavItem {
  key: 'inbox' | 'today' | 'calendar' | 'pages' | 'workspaces' | 'settings';
  icon: LucideIcon;
  /** Destination route (TanStack Router path). */
  to: '/workspaces' | '/inbox' | '/today' | '/calendar' | '/pages' | '/settings/profile';
}

const NAV_ITEMS: readonly NavItem[] = [
  { key: 'inbox', icon: Inbox, to: '/inbox' },
  { key: 'today', icon: CalendarDays, to: '/today' },
  { key: 'calendar', icon: CalendarRange, to: '/calendar' },
  { key: 'pages', icon: FileText, to: '/pages' },
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
          to="/workspaces/$id/projects/$projectId"
          params={{ id: workspaceId, projectId: p.id }}
          className={cx(styles.item, styles.subItem, 'nf-focus-ring')}
          activeProps={{
            className: cx(styles.item, styles.subItem, styles.itemActive, 'nf-focus-ring'),
          }}
        >
          <Icon icon={FolderKanban} decorative />
          <span className={styles.label}>{p.name}</span>
        </Link>
      ))}
    </div>
  );
}

/**
 * WorkspaceTimeboxesLink — single sidebar entry that points at
 * `/workspaces/{id}/timeboxes`. Rendered inline beneath the
 * workspaces nav root so the route is reachable without opening the
 * top-bar workspace switcher first. Static label, no data fetch.
 */
function WorkspaceTimeboxesLink({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('common');
  return (
    <Link
      to="/workspaces/$id/timeboxes"
      params={{ id: workspaceId }}
      className={cx(styles.item, styles.subItem, 'nf-focus-ring')}
      activeProps={{
        className: cx(styles.item, styles.subItem, styles.itemActive, 'nf-focus-ring'),
      }}
    >
      <Icon icon={Timer} decorative />
      <span className={styles.label}>{t('nav.timeboxes')}</span>
    </Link>
  );
}

/**
 * WorkspaceActivityLink — single sidebar entry that points at
 * `/workspaces/{id}/activity`. Sits beside the Timeboxes link so the
 * unified activity feed (audit + ai + mcp) is reachable directly from the
 * workspace nav. Static label, no data fetch.
 */
function WorkspaceActivityLink({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('common');
  return (
    <Link
      to="/workspaces/$id/activity"
      params={{ id: workspaceId }}
      className={cx(styles.item, styles.subItem, 'nf-focus-ring')}
      activeProps={{
        className: cx(styles.item, styles.subItem, styles.itemActive, 'nf-focus-ring'),
      }}
    >
      <Icon icon={Activity} decorative />
      <span className={styles.label}>{t('nav.activity')}</span>
    </Link>
  );
}

/**
 * WorkspaceInsightsPriorityLink — single sidebar entry that points at
 * `/workspaces/{id}/insights/priority`. Renders alongside the workspace
 * sub-section so the AI priority suggestions surface is reachable
 * without a dedicated Insights hub. Static label, no data fetch.
 */
function WorkspaceInsightsPriorityLink({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('common');
  return (
    <Link
      to="/workspaces/$id/insights/priority"
      params={{ id: workspaceId }}
      className={cx(styles.item, styles.subItem, 'nf-focus-ring')}
      activeProps={{
        className: cx(styles.item, styles.subItem, styles.itemActive, 'nf-focus-ring'),
      }}
    >
      <Icon icon={ListOrdered} decorative />
      <span className={styles.label}>{t('nav.insightsPriority')}</span>
    </Link>
  );
}

/**
 * Map a favorite target type to its sidebar icon. Lens / page /
 * timebox surfaces are a near-future addition; default to the same
 * icon used for lenses for any unknown type so the row still renders
 * recognisably.
 */
function favoriteIconFor(targetType: string): LucideIcon {
  switch (targetType) {
    case 'task':
      return CheckSquare;
    case 'project':
      return FolderKanban;
    case 'lens':
      return Eye;
    default:
      return Eye;
  }
}

/**
 * Render a single favorite as a sidebar Link. Returns `null` when the
 * favorite's target type does not yet have a resolvable route (page /
 * timebox / unknown), so the section quietly skips them rather than
 * crashing the router with an invalid `to`.
 */
function FavoriteRow({
  favorite,
  workspaceId,
}: {
  favorite: Favorite;
  workspaceId: string;
}): ReactElement | null {
  const icon = favoriteIconFor(favorite.targetType);
  const className = cx(styles.item, styles.subItem, 'nf-focus-ring');
  const activeClassName = cx(styles.item, styles.subItem, styles.itemActive, 'nf-focus-ring');
  if (favorite.targetType === 'task') {
    return (
      <Link
        to="/tasks/$taskId"
        params={{ taskId: favorite.targetId }}
        className={className}
        activeProps={{ className: activeClassName }}
      >
        <Icon icon={icon} decorative />
        <span className={styles.label}>{favorite.targetId}</span>
      </Link>
    );
  }
  if (favorite.targetType === 'project') {
    return (
      <Link
        to="/workspaces/$id/projects/$projectId"
        params={{ id: workspaceId, projectId: favorite.targetId }}
        className={className}
        activeProps={{ className: activeClassName }}
      >
        <Icon icon={icon} decorative />
        <span className={styles.label}>{favorite.targetId}</span>
      </Link>
    );
  }
  return null;
}

/**
 * FavoritesSection — renders the user's favorite tasks / projects as
 * Sidebar entries scoped to the current workspace. Suspends on first
 * load; the outer Sidebar wraps this in a Suspense with a null
 * fallback so the rest of the nav renders immediately.
 */
function FavoritesSection({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('labels');
  const { data: favorites } = useFavoritesQuery(workspaceId);
  if (favorites.length === 0) return <></>;
  return (
    <div className={styles.section}>
      <div className={styles.sectionLabel}>{t('favorites.sidebar_title')}</div>
      {favorites.map((f) => (
        <FavoriteRow key={f.id} favorite={f} workspaceId={workspaceId} />
      ))}
    </div>
  );
}

function useIsMobile(): boolean {
  const [mobile, setMobile] = useState(
    () => typeof window !== 'undefined' && window.innerWidth < BP.md,
  );
  useEffect(() => {
    const mq = window.matchMedia(`(max-width: ${BP.md - 1}px)`);
    const onChange = (e: MediaQueryListEvent): void => setMobile(e.matches);
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, []);
  return mobile;
}

export default function Sidebar(): ReactElement {
  const { t } = useTranslation('common');
  const [collapsed, setCollapsed] = useState<boolean>(() => readInitialCollapsed());
  const [mobileOpen, setMobileOpen] = useState(false);
  const isMobile = useIsMobile();
  const currentWorkspaceId = useCurrentWorkspaceId();
  const pathname = useRouterState({ select: (s) => s.location.pathname });

  useEffect(() => {
    try {
      window.localStorage.setItem(STORAGE_KEY, collapsed ? '1' : '0');
    } catch {
      /* ignore */
    }
  }, [collapsed]);

  // Close mobile drawer on route change
  // biome-ignore lint/correctness/useExhaustiveDependencies: intentionally re-run on pathname change
  useEffect(() => {
    setMobileOpen(false);
  }, [pathname]);

  const handleToggle = useCallback((): void => {
    if (isMobile) {
      setMobileOpen((prev) => !prev);
    } else {
      setCollapsed((prev) => !prev);
    }
  }, [isMobile]);

  const labelKeyFor = (
    key: NavItem['key'],
  ):
    | 'nav.inbox'
    | 'nav.today'
    | 'nav.calendar'
    | 'nav.pages'
    | 'nav.workspaces'
    | 'nav.settings' => {
    switch (key) {
      case 'inbox':
        return 'nav.inbox';
      case 'today':
        return 'nav.today';
      case 'calendar':
        return 'nav.calendar';
      case 'pages':
        return 'nav.pages';
      case 'workspaces':
        return 'nav.workspaces';
      case 'settings':
        return 'nav.settings';
    }
  };

  return (
    <>
      {/* Mobile backdrop */}
      {isMobile && mobileOpen ? (
        <div
          className={styles.backdrop}
          onClick={() => setMobileOpen(false)}
          onKeyDown={(e) => {
            if (e.key === 'Escape') setMobileOpen(false);
          }}
          role="presentation"
        />
      ) : null}
      {/* Mobile hamburger button */}
      {isMobile && !mobileOpen ? (
        <button
          type="button"
          className={styles.mobileMenuBtn}
          onClick={() => setMobileOpen(true)}
          aria-label={t('nav.expand')}
        >
          <Menu size={20} />
        </button>
      ) : null}
      <aside
        className={cx(
          styles.sidebar,
          collapsed && styles.collapsed,
          isMobile && mobileOpen && styles.mobileOpen,
        )}
        data-collapsed={collapsed || undefined}
      >
        <div className={styles.brand}>
          <span className={styles.brandLabel}>nodate-flow</span>
        </div>
        {!collapsed ? (
          <div className={styles.workspaceSwitcherSlot}>
            <Suspense fallback={null}>
              <WorkspaceSwitcher />
            </Suspense>
          </div>
        ) : null}
        <nav className={styles.nav} aria-label={t('nav.primary')}>
          {NAV_ITEMS.map((item) => {
            const label = t(labelKeyFor(item.key));
            // Settings and Workspaces should stay highlighted on any
            // nested child route (e.g. /settings/security,
            // /workspaces/$id/projects), but the Settings link targets
            // /settings/profile specifically so the default exact match
            // fails on siblings. Fall back to a pathname-prefix check.
            const sectionActive =
              (item.key === 'settings' && pathname.startsWith('/settings')) ||
              (item.key === 'workspaces' && pathname.startsWith('/workspaces'));
            const linkEl = (
              <Link
                key={item.key}
                to={item.to}
                aria-label={label}
                title={label}
                className={cx(styles.item, sectionActive && styles.itemActive, 'nf-focus-ring')}
                activeProps={{ className: cx(styles.item, styles.itemActive, 'nf-focus-ring') }}
              >
                <Icon icon={item.icon} decorative />
                <span className={styles.label}>{label}</span>
              </Link>
            );
            // Render the project tree directly beneath the Workspaces
            // nav entry when we are inside a workspace-scoped route.
            // The Favorites section is mounted alongside so starred
            // tasks / projects sit under the same workspace header.
            // The Timeboxes link is hand-coded so it is always visible
            // even when the workspace has zero projects and the
            // suspense-driven `WorkspaceProjectsSection` returns
            // nothing.
            if (item.key === 'workspaces' && currentWorkspaceId && !collapsed) {
              return (
                <div key={item.key}>
                  {linkEl}
                  <WorkspaceActivityLink workspaceId={currentWorkspaceId} />
                  <WorkspaceTimeboxesLink workspaceId={currentWorkspaceId} />
                  <WorkspaceInsightsPriorityLink workspaceId={currentWorkspaceId} />
                  <Suspense fallback={null}>
                    <WorkspaceProjectsSection workspaceId={currentWorkspaceId} />
                  </Suspense>
                  <Suspense fallback={null}>
                    <FavoritesSection workspaceId={currentWorkspaceId} />
                  </Suspense>
                </div>
              );
            }
            if (collapsed) {
              return (
                <Tooltip key={item.key} content={label} placement="right">
                  {linkEl}
                </Tooltip>
              );
            }
            return linkEl;
          })}
        </nav>
        <div className={styles.footer}>
          <button
            type="button"
            className={cx(styles.toggle, 'nf-focus-ring')}
            onClick={handleToggle}
            aria-label={collapsed ? t('nav.expand') : t('nav.collapse')}
            aria-expanded={!collapsed}
          >
            <Icon icon={isMobile ? X : collapsed ? ChevronsRight : ChevronsLeft} decorative />
          </button>
        </div>
      </aside>
    </>
  );
}
