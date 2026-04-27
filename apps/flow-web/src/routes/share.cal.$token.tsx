/**
 * /share/cal/$token — standalone public page for viewing a shared
 * calendar. No authentication required; served from flow-web. The
 * actual UI lives in the public-shares feature so this route file
 * stays a thin param bridge.
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import PublicShareCalPage from '../features/public-shares/public-share-cal-page';

function PublicShareCalRoute(): ReactElement {
  const { token } = Route.useParams();
  return <PublicShareCalPage token={token} />;
}

export const Route = createFileRoute('/share/cal/$token')({
  component: PublicShareCalRoute,
});
