/**
 * /workspaces/$id/settings — pathless layout for workspace settings (lazy).
 *
 * Renders a left sub-nav (general / mcp tokens / ai providers) and an
 * <Outlet /> for the active sub-route.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import { Link, Outlet, createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings');

type SubNavKey =
  | 'general'
  | 'mcp_tokens'
  | 'ai_providers'
  | 'ai_agents'
  | 'ai_activity'
  | 'weekly_digest'
  | 'audit_log';

interface SubNavItem {
  key: SubNavKey;
  to:
    | '/workspaces/$id/settings/general'
    | '/workspaces/$id/settings/mcp-tokens'
    | '/workspaces/$id/settings/ai-providers'
    | '/workspaces/$id/settings/ai-agents'
    | '/workspaces/$id/settings/ai-activity'
    | '/workspaces/$id/settings/weekly-digest'
    | '/workspaces/$id/settings/audit-log';
}

const SUB_NAV: readonly SubNavItem[] = [
  { key: 'general', to: '/workspaces/$id/settings/general' },
  { key: 'mcp_tokens', to: '/workspaces/$id/settings/mcp-tokens' },
  { key: 'ai_providers', to: '/workspaces/$id/settings/ai-providers' },
  { key: 'ai_agents', to: '/workspaces/$id/settings/ai-agents' },
  { key: 'ai_activity', to: '/workspaces/$id/settings/ai-activity' },
  { key: 'weekly_digest', to: '/workspaces/$id/settings/weekly-digest' },
  { key: 'audit_log', to: '/workspaces/$id/settings/audit-log' },
];

function labelKeyFor(
  key: SubNavKey,
):
  | 'nav.general'
  | 'nav.mcp_tokens'
  | 'nav.ai_providers'
  | 'nav.ai_agents'
  | 'nav.ai_activity'
  | 'nav.weekly_digest'
  | 'nav.audit_log' {
  switch (key) {
    case 'general':
      return 'nav.general';
    case 'mcp_tokens':
      return 'nav.mcp_tokens';
    case 'ai_providers':
      return 'nav.ai_providers';
    case 'ai_agents':
      return 'nav.ai_agents';
    case 'ai_activity':
      return 'nav.ai_activity';
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
        gap: '2rem',
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
        maxInlineSize: '72rem',
        marginInline: 'auto',
        inlineSize: '100%',
      }}
    >
      <nav
        aria-label={t('sections_label')}
        style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}
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
                background: 'var(--nf-color-accent-subtle, rgba(155,89,182,0.12))',
                color: 'var(--nf-color-accent, var(--color-accent, #9b59b6))',
                fontWeight: 500,
              },
            }}
            style={{
              display: 'block',
              padding: '0.5rem 0.75rem',
              borderRadius: '0.5rem',
              color: 'var(--color-fg)',
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
