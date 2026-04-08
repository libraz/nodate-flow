/**
 * WorkspaceDetail — header + tabs (Overview / Members / Settings).
 */

import Card from '@nodate-flow/ui/primitives/card';
import Tabs, { type TabItem } from '@nodate-flow/ui/primitives/tabs';
import { type ReactElement, Suspense, useId } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspaceQuery } from './api';
import WorkspaceMembersTable from './workspace-members-table';

export interface WorkspaceDetailProps {
  id: string;
}

function OverviewPanel(): ReactElement {
  const { t } = useTranslation('common');
  return (
    <Card style={{ padding: '1.5rem' }}>
      <p style={{ margin: 0, color: 'var(--color-muted)' }}>
        {t('workspaces.detail.overview_empty')}
      </p>
    </Card>
  );
}

export default function WorkspaceDetail({ id }: WorkspaceDetailProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspace } = useWorkspaceQuery(id);

  // Settings is reachable via the workspace-level sub-nav route
  // (/workspaces/$id/settings). Keeping it as a tab here too would
  // duplicate the route and confuse deep-linking, so the overview
  // shows Overview + Members only.
  const items: TabItem[] = [
    {
      value: 'overview',
      label: t('workspaces.detail.tabs.overview'),
      content: (
        <Suspense fallback={null}>
          <OverviewPanel />
        </Suspense>
      ),
    },
    {
      value: 'members',
      label: t('workspaces.detail.tabs.members'),
      content: (
        <Suspense fallback={null}>
          <WorkspaceMembersTable workspaceId={id} />
        </Suspense>
      ),
    },
  ];

  const headingId = useId();

  return (
    <article
      aria-labelledby={headingId}
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
      }}
    >
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h1
          id={headingId}
          style={{
            fontFamily: 'var(--font-display)',
            fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
            margin: 0,
          }}
        >
          {workspace.name}
        </h1>
        {workspace.description ? (
          <p style={{ margin: 0, color: 'var(--color-muted)' }}>{workspace.description}</p>
        ) : null}
      </header>

      <Tabs items={items} defaultValue="overview" aria-label={t('workspaces.detail.tabs.label')} />
    </article>
  );
}
