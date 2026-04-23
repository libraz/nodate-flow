/**
 * TopBarBreadcrumb — renders the workspace → project → view crumb trail
 * in the top-bar center column. Returns `null` on task-detail routes
 * (the task panel has its own breadcrumb) and on any route where the
 * workspace id cannot be resolved.
 *
 * Must be wrapped in `<Suspense>` by the caller; this component uses
 * the shared suspense-backed workspace / project queries so first
 * mount for an unseen workspace can suspend.
 */

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbSeparator,
} from '@nodate-flow/ui/primitives/breadcrumb';
import { Link } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { Fragment } from 'react';
import { useTranslation } from 'react-i18next';

import { useProjectQuery } from '../../features/projects/api';
import { useWorkspaceQuery } from '../../features/workspaces/api';
import styles from './top-bar.module.css';
import { PROJECT_VIEW_KEY, type ProjectView, useTopBarBreadcrumb } from './use-top-bar-breadcrumb';

export default function TopBarBreadcrumb(): ReactElement | null {
  const state = useTopBarBreadcrumb();
  if (!state) return null;
  if (state.projectId != null) {
    return (
      <ProjectBreadcrumb
        workspaceId={state.workspaceId}
        projectId={state.projectId}
        view={state.view}
      />
    );
  }
  return <WorkspaceBreadcrumb workspaceId={state.workspaceId} />;
}

/**
 * WorkspaceBreadcrumb — workspace-only trail. Rendered on cross-
 * workspace pages (`/inbox`, `/today`, …) whenever a workspace id is
 * reachable via the persisted fallback.
 */
function WorkspaceBreadcrumb({ workspaceId }: { workspaceId: string }): ReactElement | null {
  const { t } = useTranslation('common');
  const { data: workspace } = useWorkspaceQuery(workspaceId);
  if (!workspace) return null;
  return (
    <Breadcrumb label={t('common.breadcrumb')} className={styles.breadcrumb}>
      <BreadcrumbItem asChild>
        <Link to="/workspaces/$id" params={{ id: workspaceId }}>
          {workspace.name}
        </Link>
      </BreadcrumbItem>
    </Breadcrumb>
  );
}

/**
 * ProjectBreadcrumb — workspace → project → view trail. The view crumb
 * is the current-page marker and is omitted when the view cannot be
 * identified from the route id.
 */
function ProjectBreadcrumb({
  workspaceId,
  projectId,
  view,
}: {
  workspaceId: string;
  projectId: string;
  view: ProjectView | null;
}): ReactElement | null {
  const { t } = useTranslation('common');
  const { data: workspace } = useWorkspaceQuery(workspaceId);
  const { data: project } = useProjectQuery(projectId);
  if (!workspace || !project) return null;
  const viewLabel = view ? t(PROJECT_VIEW_KEY[view]) : null;
  return (
    <Breadcrumb label={t('common.breadcrumb')} className={styles.breadcrumb}>
      <BreadcrumbItem asChild>
        <Link to="/workspaces/$id" params={{ id: workspaceId }}>
          {workspace.name}
        </Link>
      </BreadcrumbItem>
      <BreadcrumbSeparator />
      <BreadcrumbItem asChild>
        <Link
          to="/workspaces/$id/projects/$projectId/tasks"
          params={{ id: workspaceId, projectId }}
          className={styles.breadcrumbProject}
        >
          {project.name}
        </Link>
      </BreadcrumbItem>
      {viewLabel ? (
        <Fragment>
          <BreadcrumbSeparator />
          <BreadcrumbItem current>{viewLabel}</BreadcrumbItem>
        </Fragment>
      ) : null}
    </Breadcrumb>
  );
}
