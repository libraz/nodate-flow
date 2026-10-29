/**
 * WorkspaceDetail — header + tabs (Overview / Members / Settings).
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Tabs, { type TabItem } from '@nodate-flow/ui/primitives/tabs';
import motionStyles from '@nodate-flow/ui/styles/list-motion.module.css';
import { Link } from '@tanstack/react-router';
import { ChevronRight } from 'lucide-react';
import { type ReactElement, Suspense, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useProjectsQuery } from '../projects/api';
import ProjectCreateDialog from '../projects/project-create-dialog';
import { useWorkspaceQuery } from './api';
import WorkspaceMembersTable from './workspace-members-table';
import styles from './workspaces.module.css';

/** Identifiers for the workspace-detail tab panels. */
export type WorkspaceDetailTab = 'overview' | 'members';

export interface WorkspaceDetailProps {
  id: string;
  /** Controlled active tab, typically driven by the `?tab=` search param. */
  tab: WorkspaceDetailTab;
  /** Called when the user activates a different tab. Consumers persist the value. */
  onTabChange: (tab: WorkspaceDetailTab) => void;
}

/** The overview lists this many projects; the Projects section holds the rest. */
const OVERVIEW_LIMIT = 8;

function OverviewPanel({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data: projects } = useProjectsQuery(workspaceId);
  const [createOpen, setCreateOpen] = useState(false);
  const staggerStyle = useFirstLoadStagger();
  const active = projects.filter((p) => !p.isArchived);

  const createDialog = (
    <ProjectCreateDialog
      workspaceId={workspaceId}
      open={createOpen}
      onClose={() => {
        setCreateOpen(false);
      }}
    />
  );

  if (active.length === 0) {
    return (
      <Card>
        <EmptyState
          titleAs="p"
          title={t('workspaces.detail.overview_empty')}
          action={
            <Button
              variant="primary"
              onClick={() => {
                setCreateOpen(true);
              }}
            >
              {t('projects.new')}
            </Button>
          }
        />
        {createDialog}
      </Card>
    );
  }

  return (
    <Card className={styles.overviewCard}>
      <h2 className={styles.sectionHeading}>
        {t('workspaces.nav.projects')} ({active.length})
      </h2>
      <ul className={styles.projectList}>
        {active.slice(0, OVERVIEW_LIMIT).map((p, index) => (
          <li key={p.id} className={motionStyles.enter} style={staggerStyle(index)}>
            <Link
              to="/workspaces/$id/projects/$projectId"
              params={{ id: workspaceId, projectId: p.id }}
              className={styles.projectLink}
              title={p.description ? `${p.name}\n${p.description}` : p.name}
            >
              <span
                aria-hidden
                className={styles.projectDot}
                style={p.color ? { background: p.color } : undefined}
              />
              <span className={styles.projectName}>{p.name}</span>
              {p.description ? (
                <span className={styles.projectDescription}>{p.description}</span>
              ) : null}
              <ChevronRight aria-hidden size={16} className={styles.projectChevron} />
            </Link>
          </li>
        ))}
      </ul>
      {active.length > OVERVIEW_LIMIT ? (
        <Link to="/workspaces/$id/projects" params={{ id: workspaceId }} className={styles.viewAll}>
          {t('workspaces.nav.projects')} <span aria-hidden>→</span>
        </Link>
      ) : null}
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
        <Suspense fallback={<Skeleton className={styles.panelSkeleton} />}>
          <OverviewPanel workspaceId={id} />
        </Suspense>
      ),
    },
    {
      value: 'members',
      label: t('workspaces.detail.tabs.members'),
      content: (
        <Suspense fallback={<Skeleton className={styles.panelSkeleton} />}>
          <WorkspaceMembersTable workspaceId={id} />
        </Suspense>
      ),
    },
  ];

  const headingId = useId();

  return (
    <article aria-labelledby={headingId} className={styles.detail}>
      <header className={styles.detailHeader}>
        <h1 id={headingId} className={styles.detailTitle}>
          {workspace.name}
        </h1>
        {workspace.description ? (
          <p className={styles.detailDescription}>{workspace.description}</p>
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
