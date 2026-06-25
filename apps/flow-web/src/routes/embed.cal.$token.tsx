/**
 * /embed/cal/$token — chromeless public calendar share for `<iframe>`
 * embedding. No authentication, no nav, no brand chrome; served from
 * flow-web. The UI lives in the public-shares feature so this route file
 * stays a thin param bridge, mirroring /share/cal/$token.
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import PublicShareEmbedPage from '../features/public-shares/public-share-embed-page';

function PublicShareEmbedRoute(): ReactElement {
  const { token } = Route.useParams();
  return <PublicShareEmbedPage token={token} />;
}

export const Route = createFileRoute('/embed/cal/$token')({
  component: PublicShareEmbedRoute,
});
