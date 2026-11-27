/**
 * /workspaces/$id/settings/ai — AI settings layout (lazy).
 *
 * One page for everything AI: what works now (status card), a note that
 * agents are optional, and route-linked tabs for connection, automation,
 * agents and usage. Connection and automation are admin-only and hidden
 * from everyone else, including while the role is unknown.
 */

import { createLazyFileRoute, getRouteApi, Link, Outlet } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import PageHeader from '../components/page-header';
import AiStatusCard from '../features/ai-providers/ai-status-card';
import styles from '../features/workspaces/workspaces.module.css';
import { isWorkspaceAdmin, useCurrentWorkspaceRole } from '../lib/use-current-workspace-role';
import layoutStyles from './_authenticated.workspaces.$id.settings.ai.module.css';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai');

function AiSettingsLayout(): ReactElement {
  const { t } = useTranslation('settings');
  const { id } = routeApi.useParams();
  const admin = isWorkspaceAdmin(useCurrentWorkspaceRole());

  const tabLinkProps = {
    params: { id },
    activeProps: { 'aria-current': 'page', 'data-active': 'true' } as const,
    className: styles.subNavLink,
  };

  return (
    <div className={layoutStyles.root}>
      <PageHeader
        title={t('ai_page.title')}
        description={t('ai_page.description')}
        help="workspace-settings-ai"
      />
      <AiStatusCard workspaceId={id} />
      <p className={layoutStyles.note}>{t('ai_page.agents_note')}</p>
      <nav aria-label={t('ai_page.tabs_label')} className={styles.subNav}>
        {admin ? (
          <Link to="/workspaces/$id/settings/ai/connection" {...tabLinkProps}>
            {t('ai_page.tabs.connection')}
          </Link>
        ) : null}
        {admin ? (
          <Link to="/workspaces/$id/settings/ai/automation" {...tabLinkProps}>
            {t('ai_page.tabs.automation')}
          </Link>
        ) : null}
        <Link to="/workspaces/$id/settings/ai/agents" {...tabLinkProps}>
          {t('ai_page.tabs.agents')}
        </Link>
        <Link to="/workspaces/$id/settings/ai/usage" {...tabLinkProps}>
          {t('ai_page.tabs.usage')}
        </Link>
      </nav>
      <div className={layoutStyles.tabContent}>
        <Outlet />
      </div>
    </div>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai')({
  component: AiSettingsLayout,
});
