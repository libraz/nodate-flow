/**
 * /workspaces/$id/insights/priority — workspace-scoped AI priority
 * suggestions surface (lazy). Lists priority adjustment proposals
 * produced by `GET /workspaces/{wsId}/ai/priority-suggestions`, with
 * Apply (mutates the task via `PATCH /tasks/{id}`) and Dismiss
 * (local-only) actions.
 *
 * There is no Insights hub yet; this route is reachable from the
 * sidebar's "Insights" entry directly.
 */

import { createLazyFileRoute } from '@tanstack/react-router';

import PriorityPage from '../features/ai-priority/priority-page';

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/insights/priority')({
  component: PriorityPage,
});
