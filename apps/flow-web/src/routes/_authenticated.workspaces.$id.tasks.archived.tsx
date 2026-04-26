/**
 * /workspaces/$id/tasks/archived — route stub. See sibling `.lazy.tsx`
 * for the page component; the archive surface is code-split from the
 * main task views because it pulls in `@tanstack/react-virtual` and a
 * dedicated set of chapter / row components.
 */

import { createFileRoute } from '@tanstack/react-router';

export const Route = createFileRoute('/_authenticated/workspaces/$id/tasks/archived')({});
