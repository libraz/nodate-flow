/**
 * /projects/$projectId/tasks — route stub. See sibling `.lazy.tsx`.
 *
 * Accepts an optional `?new=1` search param so entry points like the
 * command palette and the dock can deep-link into "open the create
 * task dialog immediately on arrival".
 */

import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';

const searchSchema = z.object({
  new: z.coerce.boolean().optional(),
});

export const Route = createFileRoute('/_authenticated/projects/$projectId/tasks')({
  validateSearch: (raw) => searchSchema.parse(raw),
});
