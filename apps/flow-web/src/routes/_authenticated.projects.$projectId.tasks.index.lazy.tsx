/**
 * /projects/$projectId/tasks/ — renders the active view (board, list, or graph)
 * driven by the persisted `useTaskView` toggle (lazy).
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import ProjectStateGraph from '../features/constraints/project-state-graph';
import TaskBoardView from '../features/tasks/task-board-view';
import TaskListView from '../features/tasks/task-list-view';
import TaskSpreadsheetView from '../features/tasks/task-spreadsheet-view';
import { useTaskView } from '../features/tasks/use-task-view';

const routeApi = getRouteApi('/_authenticated/projects/$projectId/tasks/');

function TasksIndex(): ReactElement {
  const { projectId } = routeApi.useParams();
  const view = useTaskView();
  if (view === 'graph') return <ProjectStateGraph projectId={projectId} />;
  if (view === 'spreadsheet') return <TaskSpreadsheetView projectId={projectId} />;
  return view === 'board' ? (
    <TaskBoardView projectId={projectId} />
  ) : (
    <TaskListView projectId={projectId} />
  );
}

export const Route = createLazyFileRoute('/_authenticated/projects/$projectId/tasks/')({
  component: TasksIndex,
});
