/**
 * ProjectList — renders the workspace's projects in a DataGrid.
 *
 * Rows link to /workspaces/$id/projects/$projectId. The header hosts a
 * "New project" button which opens the create dialog.
 */

import Button from '@nodate-flow/ui/primitives/button';
import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import { Link } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatEpoch } from '../../lib/format';
import { type Project, useProjectsQuery } from './api';
import ProjectCreateDialog from './project-create-dialog';

export interface ProjectListProps {
  workspaceId: string;
}

export default function ProjectList({ workspaceId }: ProjectListProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: projects } = useProjectsQuery(workspaceId);
  const [createOpen, setCreateOpen] = useState(false);

  const locale = i18n.resolvedLanguage ?? 'en';

  const columns: ColumnDef<Project, unknown>[] = [
    {
      id: 'name',
      accessorKey: 'name',
      header: () => t('projects.columns.name'),
      cell: ({ row }) => (
        <Link
          to="/workspaces/$id/projects/$projectId"
          params={{ id: workspaceId, projectId: row.original.id }}
          style={{
            color: 'var(--nf-color-fg)',
            textDecoration: 'none',
            fontWeight: 500,
          }}
        >
          {row.original.name}
        </Link>
      ),
    },
    {
      id: 'slug',
      accessorKey: 'slug',
      header: () => t('projects.columns.key'),
      cell: ({ row }) => <code>{row.original.slug}</code>,
    },
    {
      id: 'archived',
      accessorKey: 'isArchived',
      header: () => t('projects.columns.visibility'),
      cell: ({ row }) => (
        <span>
          {row.original.isArchived ? t('projects.status.archived') : t('projects.status.active')}
        </span>
      ),
    },
    {
      id: 'created',
      accessorKey: 'createdAt',
      header: () => t('projects.columns.created'),
      cell: ({ row }) => <span>{formatEpoch(row.original.createdAt, locale) ?? '—'}</span>,
    },
  ];

  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-6)',
        padding: 'var(--nf-space-page)',
        blockSize: '100%',
      }}
    >
      <header
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: 'var(--nf-space-4)',
        }}
      >
        <h1
          style={{
            fontFamily: 'var(--nf-font-display)',
            fontSize: 'var(--nf-text-page-title)',
            margin: 0,
          }}
        >
          {t('projects.title')}
        </h1>
        <Button
          variant="primary"
          onClick={() => {
            setCreateOpen(true);
          }}
        >
          {t('projects.new')}
        </Button>
      </header>

      <DataGrid<Project>
        aria-label={t('projects.title')}
        columns={columns}
        data={projects}
        emptyContent={t('projects.empty')}
        // nf-token-override: component dimension, not a spacing step
        style={{ minBlockSize: '20rem' }}
      />

      <ProjectCreateDialog
        workspaceId={workspaceId}
        open={createOpen}
        onClose={() => {
          setCreateOpen(false);
        }}
      />
    </section>
  );
}
