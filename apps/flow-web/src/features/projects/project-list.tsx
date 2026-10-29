/**
 * ProjectList — renders the workspace's projects in a DataGrid.
 *
 * Rows link to /workspaces/$id/projects/$projectId. The header hosts a
 * "New project" button which opens the create dialog.
 */

import Button from '@nodate-flow/ui/primitives/button';
import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import { Link } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatEpoch } from '../../lib/format';
import styles from '../workspaces/workspaces.module.css';
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

  const handleCreate = (): void => {
    setCreateOpen(true);
  };

  const columns: ColumnDef<Project, unknown>[] = [
    {
      id: 'name',
      accessorKey: 'name',
      header: () => t('projects.columns.name'),
      cell: ({ row }) => (
        <Link
          to="/workspaces/$id/projects/$projectId"
          params={{ id: workspaceId, projectId: row.original.id }}
          className={styles.nameLink}
          title={row.original.name}
        >
          {row.original.name}
        </Link>
      ),
    },
    {
      id: 'slug',
      accessorKey: 'slug',
      header: () => t('projects.columns.key'),
      cell: ({ row }) => <code title={row.original.slug}>{row.original.slug}</code>,
    },
    {
      id: 'archived',
      accessorKey: 'isArchived',
      header: () => t('projects.columns.visibility'),
      cell: ({ row }) => (
        <span className={row.original.isArchived ? styles.muted : undefined}>
          {row.original.isArchived ? t('projects.status.archived') : t('projects.status.active')}
        </span>
      ),
    },
    {
      id: 'created',
      accessorKey: 'createdAt',
      header: () => t('projects.columns.created'),
      cell: ({ row }) => (
        <span className={styles.numeric}>{formatEpoch(row.original.createdAt, locale) ?? '—'}</span>
      ),
    },
  ];

  return (
    <section className={styles.section}>
      <header className={styles.pageHeader}>
        <h1 className={styles.pageTitle}>{t('projects.title')}</h1>
        <Button variant="primary" onClick={handleCreate}>
          {t('projects.new')}
        </Button>
      </header>

      <DataGrid<Project>
        aria-label={t('projects.title')}
        columns={columns}
        data={projects}
        getRowId={(row) => row.id}
        emptyContent={
          <EmptyState
            titleAs="p"
            title={t('projects.empty')}
            action={
              <Button variant="default" onClick={handleCreate}>
                {t('projects.new')}
              </Button>
            }
          />
        }
        className={styles.grid}
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
