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

function PagesLayout(): ReactElement {
  return (
    <Suspense
      fallback={
        <div
          style={{
            padding: 'clamp(var(--nf-space-6), 4vw, var(--nf-space-10))',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-4)',
          }}
        >
          <Skeleton style={{ blockSize: '2rem', inlineSize: '12rem' }} />
          <Skeleton style={{ blockSize: '24rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <Outlet />
    </Suspense>
  );
}

export const Route = createFileRoute('/_authenticated/pages')({
  component: PagesLayout,
});
