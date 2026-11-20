import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import { usePresence } from '@nodate-flow/ui/hooks/use-presence';
import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import { BrandImage } from '@nodate-flow/ui/primitives';
import BrandMark from '@nodate-flow/ui/primitives/brand-mark';
import Collapse from '@nodate-flow/ui/primitives/collapse';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { BP } from '@nodate-flow/ui/tokens/breakpoints';
import { Link, useRouterState } from '@tanstack/react-router';
import {
  CheckSquare,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  Eye,
  FolderKanban,
  Keyboard,
  type LucideIcon,
  Menu,
  Plus,
  X,
} from 'lucide-react';
import { type ReactElement, Suspense, useCallback, useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type Favorite, useFavoritesQuery } from '../../features/favorites/api';
import { useProjectsQuery } from '../../features/projects/api';
import ProjectCreateDialog from '../../features/projects/project-create-dialog';
import { useWorkspacesQuery } from '../../features/workspaces/api';
import { APP_ICON, APP_LOGO, APP_NAME } from '../../lib/brand';
import { useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import { isWorkspaceAdmin, useCurrentWorkspaceRole } from '../../lib/use-current-workspace-role';
import { OPEN_SHORTCUTS_HELP_EVENT } from '../page-help';
import { type NavEntry, navEntriesIn, navEntry, navHref } from './nav-model';
import styles from './sidebar.module.css';
import WorkspaceSwitcher from './workspace-switcher';

const STORAGE_KEY = 'nf.sidebar-collapsed';
const LEGACY_STORAGE_KEY = 'nf:sidebar-collapsed';

const MORE_STORAGE_KEY = 'nf.sidebar-more-open';

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
 * One navigation destination from `NAV_ENTRIES`. `forcedActive` overrides the
 * router's own active state for the entries whose section spans more than
 * their own path (personal and workspace settings).
 */
function NavLinkItem({
  entry,
  workspaceId,
  railCollapsed,
  exact = false,
  forcedActive,
  sub = false,
}: {
  entry: NavEntry;
  workspaceId: string | null;
  railCollapsed: boolean;
  exact?: boolean;
  forcedActive?: boolean;
  sub?: boolean;
}): ReactElement {
  const { t } = useTranslation('common');
  const label = t(entry.labelKey);
  const hint = entry.hintKey ? t(entry.hintKey) : null;
  const tooltip = railCollapsed ? label : hint;
  const baseClass = cx(styles.item, sub && styles.subItem);
  const link = (
    <Link
      to={entry.route}
      {...(entry.needsWorkspace && workspaceId ? { params: { id: workspaceId } } : {})}
      aria-label={label}
      title={tooltip === null ? label : undefined}
      className={cx(baseClass, forcedActive && styles.itemActive, 'nf-focus-ring')}
      activeOptions={{ exact }}
      activeProps={{ className: cx(baseClass, styles.itemActive, 'nf-focus-ring') }}
    >
      <Icon icon={entry.icon} decorative />
      <span className={styles.label}>{label}</span>
    </Link>
  );
  if (tooltip === null) return link;
  return (
    <Tooltip content={tooltip} placement="right">
      {link}
    </Tooltip>
  );
}

/** Non-workspace-scoped entries are looked up once; the model is static. */
const PERSONAL_ENTRIES = navEntriesIn('personal');
const WORKSPACE_ENTRIES = navEntriesIn('workspace');
const MORE_ENTRIES = navEntriesIn('more');
const PROJECTS_ENTRY = navEntry('projects');
const WORKSPACE_SETTINGS_ENTRY = navEntry('workspaceSettings');
const SETTINGS_ENTRY = navEntry('settings');

/**
 * Non-archived projects of the current workspace, hanging off the Projects
 * entry. Suspends on first load; the caller wraps it in a null-fallback
 * Suspense so the rest of the nav renders immediately.
 */
function WorkspaceProjectsSection({ workspaceId }: { workspaceId: string }): ReactElement | null {
  const { data: projects } = useProjectsQuery(workspaceId);
  const visible = projects.filter((p) => !p.isArchived);
  if (visible.length === 0) return null;
  return (
    <div className={styles.subNav}>
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
  const { t } = useTranslation('labels');
  const icon = favoriteIconFor(favorite.targetType);
  const className = cx(styles.item, styles.subItem, 'nf-focus-ring');
  const activeClassName = cx(styles.item, styles.subItem, styles.itemActive, 'nf-focus-ring');
  if (favorite.targetType === 'task') {
    const fallback = t('favorites.untitled_task');
    return (
      <Link
        to="/tasks/$taskId"
        params={{ taskId: favorite.targetId }}
        className={className}
        activeProps={{ className: activeClassName }}
      >
        <Icon icon={icon} decorative />
        <span className={styles.label}>{favorite.title ?? fallback}</span>
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
        <span className={styles.label}>{favorite.title ?? t('favorites.untitled_project')}</span>
      </Link>
    );
  }
  return null;
}

/**
 * FavoritesSection — renders the user's favorite tasks / projects as
 * Sidebar entries scoped to the current workspace. Suspends on first
 * load; the outer Sidebar wraps this in a Suspense with a null
 * fallback so the rest of the nav renders immediately. With no
 * favorites it says where the star is, so the section is discoverable.
 */
function FavoritesSection({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('labels');
  const { data: favorites } = useFavoritesQuery(workspaceId);
  return (
    <div className={styles.section}>
      <div className={styles.sectionLabel} title={t('favorites.sidebar_title')}>
        {t('favorites.sidebar_title')}
      </div>
      {favorites.length === 0 ? (
        <p className={styles.sectionEmpty}>{t('favorites.empty')}</p>
      ) : (
        favorites.map((f) => <FavoriteRow key={f.id} favorite={f} workspaceId={workspaceId} />)
      )}
    </div>
  );
}

function readMoreOpen(pathname: string, workspaceId: string | null): boolean {
  try {
    if (window.localStorage.getItem(MORE_STORAGE_KEY) === '1') return true;
  } catch {
    /* ignore */
  }
  return MORE_ENTRIES.some((e) => {
    const href = navHref(e, workspaceId);
    return href !== null && pathname.startsWith(href);
  });
}

/**
 * Workspace section: the heading (workspace name), its destinations, the
 * "More" disclosure — or, with no workspace at all, the create call-to-action.
 * Suspends on the workspace list.
 */
function WorkspaceArea({
  workspaceId,
  pathname,
  railCollapsed,
}: {
  workspaceId: string | null;
  pathname: string;
  railCollapsed: boolean;
}): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  const role = useCurrentWorkspaceRole();
  const [projectDialogOpen, setProjectDialogOpen] = useState(false);
  // Initial state only: after mount the user alone opens and closes it.
  const [moreOpen, setMoreOpen] = useState(() => readMoreOpen(pathname, workspaceId));
  const headingId = useId();
  const moreId = useId();

  if (workspaces.length === 0) {
    if (railCollapsed) {
      return (
        <Tooltip content={t('nav.noWorkspace.cta')} placement="right">
          <Link
            to="/setup"
            aria-label={t('nav.noWorkspace.cta')}
            className={cx(styles.item, 'nf-focus-ring')}
          >
            <Icon icon={Plus} decorative />
          </Link>
        </Tooltip>
      );
    }
    return (
      <section className={styles.noWorkspace}>
        <p className={styles.noWorkspaceTitle}>{t('nav.noWorkspace.title')}</p>
        <p className={styles.noWorkspaceBody}>{t('nav.noWorkspace.body')}</p>
        <Link to="/setup" className={cx(styles.cta, 'nf-focus-ring')}>
          {t('nav.noWorkspace.cta')}
        </Link>
      </section>
    );
  }

  const workspace = workspaces.find((w) => w.id === workspaceId);
  if (!workspaceId || !workspace) {
    return <div className={styles.headingSkeleton} aria-hidden="true" />;
  }

  const canCreateProject = isWorkspaceAdmin(role);
  const handleToggleMore = (): void => {
    const next = !moreOpen;
    setMoreOpen(next);
    try {
      window.localStorage.setItem(MORE_STORAGE_KEY, next ? '1' : '0');
    } catch {
      /* ignore */
    }
  };

  return (
    <section
      className={styles.workspaceSection}
      {...(railCollapsed ? { 'aria-label': workspace.name } : { 'aria-labelledby': headingId })}
    >
      {railCollapsed ? null : (
        <h2 id={headingId} className={styles.wsHeading}>
          <span className={styles.monogram} aria-hidden="true">
            {(Array.from(workspace.name.trim())[0] ?? '').toUpperCase()}
          </span>
          <span className={styles.wsHeadingName} title={workspace.name}>
            {workspace.name}
          </span>
        </h2>
      )}
      {WORKSPACE_ENTRIES.map((entry) => {
        if (entry.key !== 'projects') {
          return (
            <NavLinkItem
              key={entry.key}
              entry={entry}
              workspaceId={workspaceId}
              railCollapsed={railCollapsed}
            />
          );
        }
        return (
          <div key={entry.key} className={styles.projectsGroup}>
            <div className={styles.projectsRow}>
              <NavLinkItem
                entry={PROJECTS_ENTRY}
                workspaceId={workspaceId}
                railCollapsed={railCollapsed}
                exact
              />
              {canCreateProject && !railCollapsed ? (
                <Tooltip content={t('projects.new')} placement="right">
                  <button
                    type="button"
                    className={cx(styles.addButton, 'nf-focus-ring')}
                    aria-label={t('projects.new')}
                    onClick={() => {
                      setProjectDialogOpen(true);
                    }}
                  >
                    <Icon icon={Plus} decorative />
                  </button>
                </Tooltip>
              ) : null}
            </div>
            {railCollapsed ? null : (
              <Suspense fallback={null}>
                <WorkspaceProjectsSection workspaceId={workspaceId} />
              </Suspense>
            )}
          </div>
        );
      })}
      {railCollapsed ? null : (
        <>
          <button
            type="button"
            className={cx(styles.item, styles.moreToggle, 'nf-focus-ring')}
            aria-expanded={moreOpen}
            aria-controls={moreId}
            onClick={handleToggleMore}
          >
            <span className={cx(styles.chevron, moreOpen && styles.chevronOpen)}>
              <Icon icon={ChevronRight} decorative />
            </span>
            <span className={styles.label}>{t('nav.more')}</span>
          </button>
          <Collapse open={moreOpen} id={moreId} className={styles.subCollapse}>
            <div className={styles.subNav}>
              {MORE_ENTRIES.map((entry) => (
                <NavLinkItem
                  key={entry.key}
                  entry={entry}
                  workspaceId={workspaceId}
                  railCollapsed={false}
                  sub
                />
              ))}
            </div>
          </Collapse>
          <Suspense fallback={null}>
            <FavoritesSection workspaceId={workspaceId} />
          </Suspense>
        </>
      )}
      {canCreateProject ? (
        <ProjectCreateDialog
          workspaceId={workspaceId}
          open={projectDialogOpen}
          onClose={() => {
            setProjectDialogOpen(false);
          }}
        />
      ) : null}
    </section>
  );
}

function ShortcutsButton({ railCollapsed }: { railCollapsed: boolean }): ReactElement {
  const { t } = useTranslation('common');
  const label = t('nav.shortcuts');
  const button = (
    <button
      type="button"
      className={cx(styles.item, 'nf-focus-ring')}
      aria-label={label}
      onClick={() => {
        window.dispatchEvent(new CustomEvent(OPEN_SHORTCUTS_HELP_EVENT));
      }}
    >
      <Icon icon={Keyboard} decorative />
      <span className={styles.label}>{label}</span>
    </button>
  );
  if (!railCollapsed) return button;
  return (
    <Tooltip content={label} placement="right">
      {button}
    </Tooltip>
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
  // A persisted collapsed state must not leak into the full-width mobile drawer.
  const railCollapsed = collapsed && !isMobile;
  const currentWorkspaceId = useCurrentWorkspaceId();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const drawerOpen = isMobile && mobileOpen;
  const backdropPresence = usePresence<HTMLDivElement>(drawerOpen);
  const menuBtnPresence = usePresence<HTMLButtonElement>(isMobile && !mobileOpen);

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

  return (
    <>
      {/* Mobile backdrop */}
      {backdropPresence.mounted ? (
        // biome-ignore lint/a11y/noStaticElementInteractions: decorative click-outside backdrop for the mobile drawer; carries no accessible semantics (role="presentation"). Keyboard users dismiss via the drawer's own close control.
        <div
          ref={backdropPresence.ref}
          data-state={backdropPresence.state}
          className={styles.backdrop}
          onClick={() => setMobileOpen(false)}
          onKeyDown={(e) => {
            if (e.key === 'Escape') setMobileOpen(false);
          }}
          role="presentation"
        />
      ) : null}
      {/* Mobile hamburger button */}
      {menuBtnPresence.mounted ? (
        <button
          ref={menuBtnPresence.ref}
          data-state={menuBtnPresence.state}
          inert={!(isMobile && !mobileOpen)}
          type="button"
          className={cx(styles.mobileMenuBtn, 'nf-focus-ring')}
          onClick={() => setMobileOpen(true)}
          aria-label={t('nav.expand')}
        >
          <Menu size={20} />
        </button>
      ) : null}
      <aside
        className={cx(
          styles.sidebar,
          railCollapsed && styles.collapsed,
          isMobile && mobileOpen && styles.mobileOpen,
        )}
        data-collapsed={railCollapsed || undefined}
        // Off-canvas on mobile: keep its links out of the tab order until opened.
        inert={isMobile && !mobileOpen}
        onKeyDown={(e) => {
          if (isMobile && mobileOpen && e.key === 'Escape') setMobileOpen(false);
        }}
      >
        <div className={styles.brand}>
          {APP_LOGO ? (
            <>
              <BrandImage asset={APP_LOGO} alt={APP_NAME} className={styles.brandLogo} />
              <span className={styles.railBrand}>
                {APP_ICON ? (
                  <BrandImage asset={APP_ICON} alt="" className={styles.brandIcon} />
                ) : (
                  <BrandMark size="sm" />
                )}
                <VisuallyHidden>{APP_NAME}</VisuallyHidden>
              </span>
            </>
          ) : (
            <>
              {APP_ICON ? (
                <BrandImage asset={APP_ICON} alt="" className={styles.brandIcon} />
              ) : (
                <BrandMark size="sm" />
              )}
              <span className={styles.brandLabel} title={APP_NAME}>
                {APP_NAME}
              </span>
            </>
          )}
        </div>
        <div className={styles.workspaceSwitcherSlot}>
          <Suspense fallback={null}>
            <WorkspaceSwitcher railCollapsed={railCollapsed} />
          </Suspense>
        </div>
        <nav className={styles.nav} aria-label={t('nav.primary')}>
          <div className={styles.navScroll}>
            <div className={styles.group}>
              {PERSONAL_ENTRIES.map((entry) => (
                <NavLinkItem
                  key={entry.key}
                  entry={entry}
                  workspaceId={null}
                  railCollapsed={railCollapsed}
                  exact={entry.key === 'home'}
                />
              ))}
            </div>
            <div className={styles.divider} aria-hidden="true" />
            <Suspense fallback={<div className={styles.headingSkeleton} aria-hidden="true" />}>
              <WorkspaceArea
                workspaceId={currentWorkspaceId}
                pathname={pathname}
                railCollapsed={railCollapsed}
              />
            </Suspense>
          </div>
          <div className={styles.navFooter}>
            {currentWorkspaceId ? (
              <NavLinkItem
                entry={WORKSPACE_SETTINGS_ENTRY}
                workspaceId={currentWorkspaceId}
                railCollapsed={railCollapsed}
                forcedActive={pathname.startsWith(`/workspaces/${currentWorkspaceId}/settings`)}
              />
            ) : null}
            <NavLinkItem
              entry={SETTINGS_ENTRY}
              workspaceId={null}
              railCollapsed={railCollapsed}
              forcedActive={pathname.startsWith('/settings')}
            />
            <ShortcutsButton railCollapsed={railCollapsed} />
          </div>
        </nav>
        <div className={styles.footer}>
          <button
            type="button"
            className={cx(styles.toggle, 'nf-focus-ring')}
            onClick={handleToggle}
            aria-label={railCollapsed ? t('nav.expand') : t('nav.collapse')}
            aria-expanded={!railCollapsed}
          >
            <Icon icon={isMobile ? X : railCollapsed ? ChevronsRight : ChevronsLeft} decorative />
          </button>
        </div>
      </aside>
    </>
  );
}
