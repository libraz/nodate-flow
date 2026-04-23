/**
 * /projects/$projectId/tasks — legacy redirect stub.
 * The parent route's `beforeLoad` redirects to the canonical
 * `/workspaces/$id/projects/$projectId/tasks` URL before this
 * component would render.
 *
 * Keep the `?new=1` search validator so the router accepts the
 * incoming URL shape; the param is preserved through the redirect by
 * TanStack Router.
 */

import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';

const searchSchema = z.object({
  new: z.coerce.boolean().optional(),
});

export const Route = createFileRoute('/_authenticated/projects/$projectId/tasks')({
  validateSearch: (raw) => searchSchema.parse(raw),
});
