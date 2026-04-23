/**
 * WorkspaceDetail — header + tabs (Overview / Members / Settings).
 */

import Card from '@nodate-flow/ui/primitives/card';
import Tabs, { type TabItem } from '@nodate-flow/ui/primitives/tabs';
import { Link } from '@tanstack/react-router';
import { type ReactElement, Suspense, useId } from 'react';
import { useTranslation } from 'react-i18next';

import { useProjectsQuery } from '../projects/api';
import { useWorkspaceQuery } from './api';
import WorkspaceMembersTable from './workspace-members-table';

/** Identifiers for the workspace-detail tab panels. */
export type WorkspaceDetailTab = 'overview' | 'members';

export interface WorkspaceDetailProps {
  id: string;
  /** Controlled active tab, typically driven by the `?tab=` search param. */
  tab: WorkspaceDetailTab;
  /** Called when the user activates a different tab. Consumers persist the value. */
  onTabChange: (tab: WorkspaceDetailTab) => void;
}

function OverviewPanel({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data: projects } = useProjectsQuery(workspaceId);
  const active = projects.filter((p) => !p.isArchived);

  if (active.length === 0) {
    return (
      <Card style={{ padding: '1.5rem' }}>
        <p
          style={{
            margin: 0,
            padding: '2rem 1rem',
            textAlign: 'center',
            color: 'var(--nf-color-fg-muted, var(--nf-color-fg-muted))',
            border: '1px dashed var(--nf-color-border, var(--nf-color-border))',
            borderRadius: '0.5rem',
            background: 'var(--nf-color-bg-sunken, transparent)',
          }}
        >
          {t('workspaces.detail.overview_empty')}
        </p>
      </Card>
    );
  }

  return (
    <Card style={{ padding: '1.5rem', display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <h2
        style={{
          margin: 0,
          fontSize: '0.8125rem',
          textTransform: 'uppercase',
          letterSpacing: '0.05em',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {t('workspaces.nav.projects')} ({active.length})
      </h2>
      <ul
        style={{
          listStyle: 'none',
          margin: 0,
          padding: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: '0.375rem',
        }}
      >
        {active.slice(0, 8).map((p) => (
          <li key={p.id}>
            <Link
              to="/workspaces/$id/projects/$projectId"
              params={{ id: workspaceId, projectId: p.id }}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '0.75rem',
                padding: '0.6rem 0.75rem',
                borderRadius: '0.5rem',
                background: 'var(--nf-color-surface))',
                color: 'inherit',
                textDecoration: 'none',
              }}
            >
              <span
                aria-hidden
                style={{
                  inlineSize: '0.6rem',
                  blockSize: '0.6rem',
                  borderRadius: '999px',
                  background: p.color ?? 'var(--nf-color-fg-muted)',
                  flexShrink: 0,
                }}
              />
              <span style={{ fontWeight: 500 }}>{p.name}</span>
              {p.description ? (
                <span
                  style={{
                    color: 'var(--nf-color-fg-muted)',
                    fontSize: '0.8125rem',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                    flex: 1,
                    minWidth: 0,
                  }}
                >
                  {p.description}
                </span>
              ) : null}
            </Link>
          </li>
        ))}
      </ul>
    </Card>
  );
}

export default function WorkspaceDetail({
  id,
  tab,
  onTabChange,
}: WorkspaceDetailProps): ReactElement {
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
          <OverviewPanel workspaceId={id} />
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
          <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>{workspace.description}</p>
        ) : null}
      </header>

      <Tabs
        items={items}
        value={tab}
        onValueChange={(next) => {
          onTabChange(next as WorkspaceDetailTab);
        }}
        aria-label={t('workspaces.detail.tabs.label')}
      />
    </article>
  );
}
