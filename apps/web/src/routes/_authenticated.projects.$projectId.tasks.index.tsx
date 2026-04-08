/**
 * /projects/$projectId/tasks/ — renders the active view (board or list)
 * driven by the persisted `useTaskView` toggle.
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import TaskBoardView from '../features/tasks/task-board-view';
import TaskListView from '../features/tasks/task-list-view';
import { useTaskView } from '../features/tasks/use-task-view';

function TasksIndex(): ReactElement {
  const { projectId } = Route.useParams();
  const view = useTaskView();
  return view === 'board' ? (
    <TaskBoardView projectId={projectId} />
  ) : (
    <TaskListView projectId={projectId} />
  );
}

export const Route = createFileRoute('/_authenticated/projects/$projectId/tasks/')({
  component: TasksIndex,
});
