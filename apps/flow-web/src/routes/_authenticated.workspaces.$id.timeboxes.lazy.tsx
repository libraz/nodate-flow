/**
 * /workspaces/$id/timeboxes — workspace-scoped timeboxes surface
 * (lazy). Lists every timebox grouped by lifecycle status with
 * inline action buttons for status transitions and task linkage.
 */

import { createLazyFileRoute } from '@tanstack/react-router';

import TimeboxesPage from '../features/timeboxes/timeboxes-page';

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/timeboxes')({
  component: TimeboxesPage,
});
