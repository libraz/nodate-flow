/**
 * /workspaces/$id/projects/$projectId/ — project detail
 * (Overview / Members / Settings).
 *
 * The active tab is stored in the `?tab=` search param so reloads and
 * deep links preserve which panel the user was on. Invalid/missing
 * values fall back to `overview`.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { z } from 'zod';

import ProjectDetail, { type ProjectDetailTab } from '../features/projects/project-detail';

const TAB_VALUES = ['overview', 'members', 'settings'] as const;

const searchSchema = z.object({
  tab: z.enum(TAB_VALUES).optional().catch('overview'),
});

function ProjectDetailRoute(): ReactElement {
  const { id, projectId } = Route.useParams();
  const { tab } = Route.useSearch();
  const navigate = useNavigate();
  const activeTab: ProjectDetailTab = tab ?? 'overview';
  return (
    <Suspense
      fallback={
        <div
          style={{
            padding: 'var(--nf-space-8)',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-4)',
          }}
        >
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <ProjectDetail
        id={projectId}
        tab={activeTab}
        onTabChange={(next) => {
          void navigate({
            to: '/workspaces/$id/projects/$projectId',
            params: { id, projectId },
            search: (prev) => ({ ...prev, tab: next === 'overview' ? undefined : next }),
            replace: true,
          });
        }}
      />
    </Suspense>
  );
}

export const Route = createFileRoute('/_authenticated/workspaces/$id/projects/$projectId/')({
  component: ProjectDetailRoute,
  validateSearch: (raw) => searchSchema.parse(raw),
});
