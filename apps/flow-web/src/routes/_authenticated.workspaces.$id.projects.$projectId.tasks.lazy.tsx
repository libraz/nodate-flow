/**
 * /workspaces/$id/projects/$projectId/tasks — section layout for the
 * tasks views (lazy).
 *
 * Hosts the view switcher (Board / List), the filters bar, the
 * "New task" button, and an <Outlet /> for the active view.
 */

import Icon from '@nodate-flow/ui/icon';
import Button from '@nodate-flow/ui/primitives/button';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi, Outlet, useNavigate } from '@tanstack/react-router';
import { Plus } from 'lucide-react';
import { type ReactElement, Suspense, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useProjectQuery } from '../features/projects/api';
import ProjectSectionHeader from '../features/projects/project-section-header';
import TaskCreateDialog from '../features/tasks/task-create-dialog';
import TaskFiltersBar from '../features/tasks/task-filters-bar';
import TaskViewSwitcher from '../features/tasks/task-view-switcher';
import styles from './_authenticated.workspaces.$id.projects.$projectId.tasks.module.css';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/projects/$projectId/tasks');

function TasksSectionLayout(): ReactElement {
  const { t } = useTranslation('common');
  const { id, projectId } = routeApi.useParams();
  const search = routeApi.useSearch();
  const navigate = useNavigate();
  const { data: project } = useProjectQuery(projectId);
  const [createOpen, setCreateOpen] = useState(false);

  // Deep-link: `?new=1` opens the create-task dialog on arrival
  // (palette / dock / today empty state). Strip the param afterwards so
  // the dialog does not re-open on back navigation.
  useEffect(() => {
    if (search.new) {
      setCreateOpen(true);
      void navigate({
        to: '/workspaces/$id/projects/$projectId/tasks',
        params: { id, projectId },
        search: (prev) => ({ ...prev, new: undefined }),
        replace: true,
      });
    }
  }, [search.new, navigate, id, projectId]);

  return (
    <section className={styles.section}>
      <ProjectSectionHeader
        title={t('tasks.title')}
        help="project-tasks"
        description={t('tasks.description')}
        actions={
          <>
            <TaskViewSwitcher />
            <Button
              variant="primary"
              className={styles.newTask}
              onClick={() => {
                setCreateOpen(true);
              }}
            >
              <Icon icon={Plus} decorative />
              {t('tasks.new')}
            </Button>
          </>
        }
      />

      <TaskFiltersBar projectId={projectId} workspaceId={project.workspaceId} />

      <Suspense
        fallback={
          <div className={styles.fallback}>
            <Skeleton className={styles.fallbackToolbar} />
            <Skeleton className={styles.fallbackBody} />
          </div>
        }
      >
        <Outlet />
      </Suspense>

      <TaskCreateDialog
        projectId={projectId}
        workspaceId={project.workspaceId}
        open={createOpen}
        onClose={() => {
          setCreateOpen(false);
        }}
      />
    </section>
  );
}

export const Route = createLazyFileRoute(
  '/_authenticated/workspaces/$id/projects/$projectId/tasks',
)({
  component: TasksSectionLayout,
});
