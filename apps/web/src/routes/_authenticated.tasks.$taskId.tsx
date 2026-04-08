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

import { sdk } from '../lib/sdk';

export const Route = createFileRoute('/_authenticated/tasks/$taskId')({
  loader: async ({ params }) => {
    const { response } = await sdk.GET('/tasks/{id}', {
      params: { path: { id: params.taskId } },
    });
    if (response.status === 404) throw notFound();
    return null;
  },
});
