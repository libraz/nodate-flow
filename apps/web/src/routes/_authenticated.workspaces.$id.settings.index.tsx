/**
 * /workspaces/$id/settings — index route.
 *
 * The bare settings URL has no content of its own; redirect to the
 * General sub-page so users always land on a populated screen.
 */

import { createFileRoute, redirect } from '@tanstack/react-router';

export const Route = createFileRoute('/_authenticated/workspaces/$id/settings/')({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: '/workspaces/$id/settings/general',
      params: { id: params.id },
      replace: true,
    });
  },
});
