/**
 * /projects/$projectId/tasks — section layout for the tasks views (lazy).
 *
 * Hosts the view switcher (Board / List), the filters bar, the
 * "New task" button, and an <Outlet /> for the active view.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { Outlet, createLazyFileRoute, getRouteApi, useNavigate } from '@tanstack/react-router';
import { type ReactElement, Suspense, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useProjectQuery } from '../features/projects/api';
import TaskCreateDialog from '../features/tasks/task-create-dialog';
import TaskFiltersBar from '../features/tasks/task-filters-bar';
import TaskViewSwitcher from '../features/tasks/task-view-switcher';

const routeApi = getRouteApi('/_authenticated/projects/$projectId/tasks');

function TasksSectionLayout(): ReactElement {
  const { t } = useTranslation('common');
  const { projectId } = routeApi.useParams();
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
        to: '/projects/$projectId/tasks',
        params: { projectId },
        search: {},
        replace: true,
      });
    }
  }, [search.new, navigate, projectId]);

  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1.25rem',
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
        blockSize: '100%',
      }}
    >
      <header
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: '1rem',
          flexWrap: 'wrap',
        }}
      >
        <h1
          style={{
            fontFamily: 'var(--font-display)',
            fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
            margin: 0,
          }}
        >
          {t('tasks.title')}
        </h1>
        <div style={{ display: 'flex', gap: '0.75rem', alignItems: 'center' }}>
          <TaskViewSwitcher />
          <Button
            variant="primary"
            onClick={() => {
              setCreateOpen(true);
            }}
          >
            {t('tasks.new')}
          </Button>
        </div>
      </header>

      <TaskFiltersBar projectId={projectId} workspaceId={project.workspaceId} />

      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
            <Skeleton style={{ blockSize: '2rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <Outlet />
      </Suspense>

      <TaskCreateDialog
        projectId={projectId}
        open={createOpen}
        onClose={() => {
          setCreateOpen(false);
        }}
      />
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/projects/$projectId/tasks')({
  component: TasksSectionLayout,
});
