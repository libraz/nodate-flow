/**
 * /projects/$projectId/tasks/ — renders the active view (board or list)
 * driven by the persisted `useTaskView` toggle (lazy).
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import TaskBoardView from '../features/tasks/task-board-view';
import TaskListView from '../features/tasks/task-list-view';
import { useTaskView } from '../features/tasks/use-task-view';

const routeApi = getRouteApi('/_authenticated/projects/$projectId/tasks/');

function TasksIndex(): ReactElement {
  const { projectId } = routeApi.useParams();
  const view = useTaskView();
  return view === 'board' ? (
    <TaskBoardView projectId={projectId} />
  ) : (
    <TaskListView projectId={projectId} />
  );
}

export const Route = createLazyFileRoute('/_authenticated/projects/$projectId/tasks/')({
  component: TasksIndex,
});
