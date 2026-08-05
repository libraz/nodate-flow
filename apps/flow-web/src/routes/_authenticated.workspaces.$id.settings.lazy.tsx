/**
 * /workspaces/$id/settings — pathless layout for workspace settings (lazy).
 *
 * Renders a left sub-nav (general / mcp tokens / ai providers) and an
 * <Outlet /> for the active sub-route.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import { createLazyFileRoute, getRouteApi, Link, Outlet } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

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

const BASE_SUB_NAV: readonly SubNavItem[] = [
  { key: 'general', to: '/workspaces/$id/settings/general' },
  { key: 'data', to: '/workspaces/$id/settings/data' },
  { key: 'public_shares', to: '/workspaces/$id/settings/public-shares' },
  { key: 'mcp_tokens', to: '/workspaces/$id/settings/mcp-tokens' },
  { key: 'webhooks', to: '/workspaces/$id/settings/webhooks' },
  { key: 'ai_providers', to: '/workspaces/$id/settings/ai-providers' },
  { key: 'ai_agents', to: '/workspaces/$id/settings/ai-agents' },
  { key: 'auto_actions', to: '/workspaces/$id/settings/auto-actions' },
  { key: 'ai_activity', to: '/workspaces/$id/settings/ai-activity' },
  { key: 'ai_metrics', to: '/workspaces/$id/settings/ai/metrics' },
  { key: 'weekly_digest', to: '/workspaces/$id/settings/weekly-digest' },
];

const SUB_NAV: readonly SubNavItem[] = AUDIT_LOG_ENABLED
  ? [...BASE_SUB_NAV, { key: 'audit_log', to: '/workspaces/$id/settings/audit-log' }]
  : BASE_SUB_NAV;

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

  return (
    <section
      style={{
        display: 'grid',
        gridTemplateColumns: '16rem 1fr',
        gap: 'var(--nf-space-8)',
        padding: 'clamp(var(--nf-space-6), 4vw, var(--nf-space-10))',
        // nf-token-override: component dimension, not a spacing step
        maxInlineSize: '72rem',
        marginInline: 'auto',
        inlineSize: '100%',
      }}
    >
      <nav
        aria-label={t('sections_label')}
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1)' }}
      >
        {SUB_NAV.map((item) => (
          <Link
            key={item.key}
            to={item.to}
            params={{ id }}
            className={cx('settings-subnav-link')}
            activeProps={{
              'aria-current': 'page',
              style: {
                background: 'var(--nf-color-accent-subtle)',
                color: 'var(--nf-color-accent)',
                fontWeight: 500,
              },
            }}
            style={{
              display: 'block',
              padding: 'var(--nf-space-2) var(--nf-space-3)',
              borderRadius: 'var(--nf-radius-md)',
              color: 'var(--nf-color-fg)',
              textDecoration: 'none',
            }}
          >
            {t(labelKeyFor(item.key))}
          </Link>
        ))}
      </nav>
      <div>
        <Outlet />
      </div>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings')({
  component: WorkspaceSettingsLayout,
});
