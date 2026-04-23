/**
 * /projects/$projectId/gantt — legacy redirect stub.
 * Parent `beforeLoad` redirects to the canonical workspace-scoped URL.
 */

import { createFileRoute } from '@tanstack/react-router';

export const Route = createFileRoute('/_authenticated/projects/$projectId/gantt')({});
