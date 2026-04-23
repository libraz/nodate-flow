/**
 * /projects/$projectId/ — legacy redirect stub.
 * The parent route's `beforeLoad` redirects to the canonical
 * `/workspaces/$id/projects/$projectId` URL before this component
 * would render.
 */

import { createFileRoute } from '@tanstack/react-router';

export const Route = createFileRoute('/_authenticated/projects/$projectId/')({});
