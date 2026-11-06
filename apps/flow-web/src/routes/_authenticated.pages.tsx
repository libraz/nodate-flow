/**
 * /pages — layout route for the Pages/Wiki feature.
 *
 * Wraps all pages routes in a Suspense boundary and renders the
 * nested child via <Outlet />.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute, Outlet } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { Suspense } from 'react';

import PageFrame from '../components/page-frame';

function PagesLayout(): ReactElement {
  return (
    <Suspense
      fallback={
        <PageFrame width="full">
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '2rem', inlineSize: '12rem' }} />
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '24rem', inlineSize: '100%' }} />
        </PageFrame>
      }
    >
      <Outlet />
    </Suspense>
  );
}

export const Route = createFileRoute('/_authenticated/pages')({
  component: PagesLayout,
});
