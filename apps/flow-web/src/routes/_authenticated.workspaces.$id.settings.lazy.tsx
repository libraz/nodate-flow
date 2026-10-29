/**
 * /workspaces/$id/settings — pathless layout for workspace settings (lazy).
 *
 * Renders a left sub-nav, grouped by concern, and an <Outlet /> for the
 * active sub-route. Shares the account settings layout styles, so it
 * collapses into the same scrollable tab strip below 768px, and keys the
 * outlet on the path so each sub-page fades in on arrival.
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

import styles from './_authenticated.settings.module.css';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings');

type SubNavKey =
  | 'general'
  | 'data'
  | 'public_shares'
  | 'mcp_tokens'
  | 'webhooks'
  | 'ai_providers'
  | 'ai_agents'
  | 'auto_actions'
  | 'ai_activity'
  | 'ai_metrics'
  | 'weekly_digest'
  | 'audit_log';

interface SubNavItem {
  key: SubNavKey;
  to:
    | '/workspaces/$id/settings/general'
    | '/workspaces/$id/settings/data'
    | '/workspaces/$id/settings/public-shares'
    | '/workspaces/$id/settings/mcp-tokens'
    | '/workspaces/$id/settings/webhooks'
    | '/workspaces/$id/settings/ai-providers'
    | '/workspaces/$id/settings/ai-agents'
    | '/workspaces/$id/settings/auto-actions'
    | '/workspaces/$id/settings/ai-activity'
    | '/workspaces/$id/settings/ai/metrics'
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

type NavGroupKey = 'workspace' | 'integrations' | 'ai' | 'reports';

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
      { key: 'public_shares', to: '/workspaces/$id/settings/public-shares' },
    ],
  },
  {
    key: 'integrations',
    items: [
      { key: 'mcp_tokens', to: '/workspaces/$id/settings/mcp-tokens' },
      { key: 'webhooks', to: '/workspaces/$id/settings/webhooks' },
    ],
  },
  {
    key: 'ai',
    items: [
      { key: 'ai_providers', to: '/workspaces/$id/settings/ai-providers' },
      { key: 'ai_agents', to: '/workspaces/$id/settings/ai-agents' },
      { key: 'auto_actions', to: '/workspaces/$id/settings/auto-actions' },
      { key: 'ai_activity', to: '/workspaces/$id/settings/ai-activity' },
      { key: 'ai_metrics', to: '/workspaces/$id/settings/ai/metrics' },
    ],
  },
  {
    key: 'reports',
    items: AUDIT_LOG_ENABLED
      ? [
          { key: 'weekly_digest', to: '/workspaces/$id/settings/weekly-digest' },
          { key: 'audit_log', to: '/workspaces/$id/settings/audit-log' },
        ]
      : [{ key: 'weekly_digest', to: '/workspaces/$id/settings/weekly-digest' }],
  },
];

function groupLabelKeyFor(
  key: NavGroupKey,
): 'nav_group.workspace' | 'nav_group.integrations' | 'nav_group.ai' | 'nav_group.reports' {
  switch (key) {
    case 'workspace':
      return 'nav_group.workspace';
    case 'integrations':
      return 'nav_group.integrations';
    case 'ai':
      return 'nav_group.ai';
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
  | 'nav.ai_providers'
  | 'nav.ai_agents'
  | 'nav.auto_actions'
  | 'nav.ai_activity'
  | 'nav.ai_metrics'
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
    case 'ai_providers':
      return 'nav.ai_providers';
    case 'ai_agents':
      return 'nav.ai_agents';
    case 'auto_actions':
      return 'nav.auto_actions';
    case 'ai_activity':
      return 'nav.ai_activity';
    case 'ai_metrics':
      return 'nav.ai_metrics';
    case 'weekly_digest':
      return 'nav.weekly_digest';
    case 'audit_log':
      return 'nav.audit_log';
  }
}

function WorkspaceSettingsLayout(): ReactElement {
  const { t } = useTranslation('settings');
  const { id } = routeApi.useParams();
  const pathname = useRouterState({ select: (state) => state.location.pathname });

  return (
    <section className={styles.layout}>
      <nav aria-label={t('sections_label')} className={styles.nav}>
        {NAV_GROUPS.map((group) => (
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
      <div key={pathname} className={styles.outlet}>
        <Outlet />
      </div>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings')({
  component: WorkspaceSettingsLayout,
});
