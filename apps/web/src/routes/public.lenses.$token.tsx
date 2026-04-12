/**
 * /public/lenses/$token — standalone public page for viewing a published
 * lens. No authentication required.
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import PublicLensPage from '../features/sharing/public-lens-page';

function PublicLensRoute(): ReactElement {
  const { token } = Route.useParams();
  return <PublicLensPage token={token} />;
}

export const Route = createFileRoute('/public/lenses/$token')({
  component: PublicLensRoute,
});
