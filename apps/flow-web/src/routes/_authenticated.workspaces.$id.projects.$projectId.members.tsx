/**
 * /workspaces/$id/projects/$projectId/members — redirect shim.
 *
 * The project detail UI exposes the members panel as a tab on the
 * overview page (`?tab=members`) rather than a dedicated sub-route.
 * Users who deep-link, bookmark, or copy a `/members` URL used to land
 * on a raw untranslated "Not Found" page; this route bounces them to
 * the canonical overview + tab query so the tabs UI renders as
 * expected. Using `beforeLoad` + `throw redirect` avoids a component
 * render flash.
 */

import { createFileRoute, redirect } from '@tanstack/react-router';

export const Route = createFileRoute('/_authenticated/workspaces/$id/projects/$projectId/members')({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: '/workspaces/$id/projects/$projectId',
      params: { id: params.id, projectId: params.projectId },
      search: { tab: 'members' },
      replace: true,
    });
  },
});
