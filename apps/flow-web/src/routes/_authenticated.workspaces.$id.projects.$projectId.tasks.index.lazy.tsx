/**
 * /workspaces/$id/projects/$projectId/tasks/ — renders the active view
 * (board, list, or graph) driven by the persisted `useTaskView`
 * toggle (lazy).
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import ProjectStateGraph from '../features/constraints/project-state-graph';
import TaskBoardView from '../features/tasks/task-board-view';
import TaskListView from '../features/tasks/task-list-view';
import TaskSpreadsheetView from '../features/tasks/task-spreadsheet-view';
import { useTaskView } from '../features/tasks/use-task-view';
import css from './_authenticated.workspaces.$id.projects.$projectId.tasks.index.module.css';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/projects/$projectId/tasks/');

function TasksIndex(): ReactElement {
  const { projectId } = routeApi.useParams();
  const view = useTaskView();
  return (
    <div key={view} className={css.view}>
      {view === 'graph' ? (
        <ProjectStateGraph projectId={projectId} />
      ) : view === 'spreadsheet' ? (
        <TaskSpreadsheetView projectId={projectId} />
      ) : view === 'board' ? (
        <TaskBoardView projectId={projectId} />
      ) : (
        <TaskListView projectId={projectId} />
      )}
    </div>
  );
}

export const Route = createLazyFileRoute(
  '/_authenticated/workspaces/$id/projects/$projectId/tasks/',
)({
  component: TasksIndex,
});
