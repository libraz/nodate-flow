/**
 * /workspaces/$id/projects/$projectId/settings — rename, archive and delete.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import { SettingsPanel } from '../features/projects/project-detail';

function ProjectSettingsRoute(): ReactElement {
  const { projectId } = Route.useParams();
  return (
    <Suspense
      fallback={
        <div
          aria-busy="true"
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6)' }}
        >
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: 'var(--nf-control-height)', inlineSize: '16rem' }} />
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <SettingsPanel id={projectId} />
    </Suspense>
  );
}

export const Route = createFileRoute('/_authenticated/workspaces/$id/projects/$projectId/settings')(
  {
    component: ProjectSettingsRoute,
  },
);
