/**
 * /tasks/$taskId — route stub. Component lives in the sibling
 * `.lazy.tsx` file so the heavy task-detail bundle is code-split.
 *
 * The loader probes the task by id so deep-link 404s (deleted task,
 * typo'd id) translate into a TanStack Router `notFound()` and land on
 * the branded NotFound rendered inside the authenticated AppShell,
 * instead of bubbling a thrown `TaskApiError` up to the root
 * ErrorBoundary fallback.
 */

import { createFileRoute, notFound } from '@tanstack/react-router';

import { apiProbe } from '../lib/api';

export const Route = createFileRoute('/_authenticated/tasks/$taskId')({
  loader: async ({ params }) => {
    // The loader only asks whether the task is there; the lazy component
    // fetches it for real and reports any other failure itself.
    const status = await apiProbe((client) =>
      client.GET('/tasks/{id}', { params: { path: { id: params.taskId } } }),
    );
    if (status === 404) throw notFound();
    return null;
  },
});
