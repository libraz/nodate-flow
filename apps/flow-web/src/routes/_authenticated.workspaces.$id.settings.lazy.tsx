/**
 * /workspaces/$id/settings — pathless layout for workspace settings (lazy).
 *
 * Renders a left sub-nav, grouped by concern, and an <Outlet /> for the
 * active sub-route. Shares the account settings layout styles, so it
 * collapses into the same scrollable tab strip below 768px, and keys the
 * outlet on the first path segment after /settings/ so each sub-page fades in
 * on arrival while tabs within one page (AI) stay mounted.
 */

import {
  createLazyFileRoute,
  getRouteApi,
  Link,
  Outlet,
  useRouterState,
} from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { isWorkspaceAdmin, useCurrentWorkspaceRole } from '../lib/use-current-workspace-role';
import styles from './_authenticated.settings.module.css';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings');

type SubNavKey =
  | 'general'
  | 'data'
  | 'public_shares'
  | 'mcp_tokens'
  | 'webhooks'
  | 'ai'
  | 'weekly_digest'
  | 'audit_log';

interface SubNavItem {
  key: SubNavKey;
  /** Shown to owner / admin only (the API floor); hidden while the role is unknown. */
  adminOnly?: true;
  /** Hidden from guests (and while the role is unknown). */
  nonGuest?: true;
  to:
    | '/workspaces/$id/settings/general'
    | '/workspaces/$id/settings/data'
    | '/workspaces/$id/settings/public-shares'
    | '/workspaces/$id/settings/mcp-tokens'
    | '/workspaces/$id/settings/webhooks'
    | '/workspaces/$id/settings/ai'
    | '/workspaces/$id/settings/weekly-digest'
    | '/workspaces/$id/settings/audit-log';
}

/**
 * Audit log is a forward-looking section: the backend handler isn't
 * registered yet, so the sidebar link is hidden by default and only
 * shown when the build advertises the capability via
 * `VITE_NF_FEATURE_AUDIT_LOG=1`. The route itself remains reachable by
 * direct URL for early dev preview.
 */
const AUDIT_LOG_ENABLED = (import.meta.env.VITE_NF_FEATURE_AUDIT_LOG as string | undefined) === '1';

type NavGroupKey = 'workspace' | 'integrations' | 'reports';

interface NavGroup {
  key: NavGroupKey;
  items: readonly SubNavItem[];
}

const NAV_GROUPS: readonly NavGroup[] = [
  {
    key: 'workspace',
    items: [
      { key: 'general', to: '/workspaces/$id/settings/general' },
      { key: 'data', to: '/workspaces/$id/settings/data' },
      { key: 'public_shares', to: '/workspaces/$id/settings/public-shares', nonGuest: true },
      { key: 'ai', to: '/workspaces/$id/settings/ai' },
    ],
  },
  {
    key: 'integrations',
    items: [
      { key: 'mcp_tokens', to: '/workspaces/$id/settings/mcp-tokens' },
      { key: 'webhooks', to: '/workspaces/$id/settings/webhooks', adminOnly: true },
    ],
  },
  {
    key: 'reports',
    items: [
      { key: 'weekly_digest', to: '/workspaces/$id/settings/weekly-digest' },
      ...(AUDIT_LOG_ENABLED
        ? [
            {
              key: 'audit_log',
              to: '/workspaces/$id/settings/audit-log',
              adminOnly: true,
            } as const,
          ]
        : []),
    ],
  },
];

function groupLabelKeyFor(
  key: NavGroupKey,
): 'nav_group.workspace' | 'nav_group.integrations' | 'nav_group.reports' {
  switch (key) {
    case 'workspace':
      return 'nav_group.workspace';
    case 'integrations':
      return 'nav_group.integrations';
    case 'reports':
      return 'nav_group.reports';
  }
}

function labelKeyFor(
  key: SubNavKey,
):
  | 'nav.general'
  | 'nav.data'
  | 'nav.public_shares'
  | 'nav.mcp_tokens'
  | 'nav.webhooks'
  | 'nav.ai'
  | 'nav.weekly_digest'
  | 'nav.audit_log' {
  switch (key) {
    case 'general':
      return 'nav.general';
    case 'data':
      return 'nav.data';
    case 'public_shares':
      return 'nav.public_shares';
    case 'mcp_tokens':
      return 'nav.mcp_tokens';
    case 'webhooks':
      return 'nav.webhooks';
    case 'ai':
      return 'nav.ai';
    case 'weekly_digest':
      return 'nav.weekly_digest';
    case 'audit_log':
      return 'nav.audit_log';
  }
}

function outletKeyFor(pathname: string): string {
  const marker = '/settings/';
  const at = pathname.indexOf(marker);
  if (at < 0) return pathname;
  return pathname.slice(at + marker.length).split('/')[0] ?? '';
}

function WorkspaceSettingsLayout(): ReactElement {
  const { t } = useTranslation('settings');
  const { id } = routeApi.useParams();
  // First segment after /settings/: tab changes inside one section keep the outlet mounted.
  const outletKey = useRouterState({ select: (state) => outletKeyFor(state.location.pathname) });
  const role = useCurrentWorkspaceRole();
  const admin = isWorkspaceAdmin(role);
  const visibleGroups = NAV_GROUPS.map((group) => ({
    key: group.key,
    items: group.items.filter(
      (item) =>
        (item.adminOnly !== true || admin) &&
        (item.nonGuest !== true || (role !== null && role !== 'guest')),
    ),
  })).filter((group) => group.items.length > 0);

  return (
    <section className={styles.layout}>
      <nav aria-label={t('sections_label')} className={styles.nav}>
        {visibleGroups.map((group) => (
          <div key={group.key} className={styles.group}>
            <span className={styles.groupLabel}>{t(groupLabelKeyFor(group.key))}</span>
            {group.items.map((item) => (
              <Link
                key={item.key}
                to={item.to}
                params={{ id }}
                className={styles.link}
                activeProps={{ 'aria-current': 'page' }}
              >
                {t(labelKeyFor(item.key))}
              </Link>
            ))}
          </div>
        ))}
      </nav>
      <div key={outletKey} className={styles.outlet}>
        <Outlet />
      </div>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings')({
  component: WorkspaceSettingsLayout,
});
