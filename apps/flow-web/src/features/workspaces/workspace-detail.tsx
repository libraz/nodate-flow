/**
 * WorkspaceDetail — workspace header and project overview.
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import motionStyles from '@nodate-flow/ui/styles/list-motion.module.css';
import { Link } from '@tanstack/react-router';
import { ChevronRight } from 'lucide-react';
import { type ReactElement, Suspense, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import PageHeader from '../../components/page-header';
import { useProjectsQuery } from '../projects/api';
import ProjectCreateDialog from '../projects/project-create-dialog';
import { useWorkspaceQuery } from './api';
import styles from './workspaces.module.css';

export interface WorkspaceDetailProps {
  id: string;
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

export default function WorkspaceDetail({ id }: WorkspaceDetailProps): ReactElement {
  const { data: workspace } = useWorkspaceQuery(id);
  const headingId = useId();

  return (
    <article aria-labelledby={headingId} className={styles.detail}>
      <PageHeader titleId={headingId} title={workspace.name} description={workspace.description} />

      <Suspense fallback={<Skeleton className={styles.panelSkeleton} />}>
        <OverviewPanel workspaceId={id} />
      </Suspense>
    </article>
  );
}
