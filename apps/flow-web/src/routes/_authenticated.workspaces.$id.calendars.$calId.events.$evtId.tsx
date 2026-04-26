/**
 * /workspaces/$id/calendars/$calId/events/$evtId — route stub. See sibling
 * `.lazy.tsx` for the page component; the heavy detail bundle is code-split.
 */

import { createFileRoute } from '@tanstack/react-router';

export const Route = createFileRoute(
  '/_authenticated/workspaces/$id/calendars/$calId/events/$evtId',
)({});
