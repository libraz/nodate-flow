/**
 * WorkspaceList — renders the caller's workspaces in a DataGrid.
 *
 * Rows link to /workspaces/$id. The header hosts a "New workspace" button
 * which opens the create dialog.
 */

import Button from '@nodate-flow/ui/primitives/button';
import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import { Link } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatEpoch } from '../../lib/format';
import { useWorkspacesQuery, type Workspace } from './api';
import WorkspaceCreateDialog from './workspace-create-dialog';
import styles from './workspaces.module.css';

export default function WorkspaceList(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  const [createOpen, setCreateOpen] = useState(false);

  const locale = i18n.resolvedLanguage ?? 'en';

  const handleCreate = (): void => {
    setCreateOpen(true);
  };

  const columns: ColumnDef<Workspace, unknown>[] = [
    {
      id: 'name',
      accessorKey: 'name',
      header: () => t('workspaces.columns.name'),
      cell: ({ row }) => (
        <Link
          to="/workspaces/$id"
          params={{ id: row.original.id }}
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
      header: () => t('workspaces.columns.slug'),
      cell: ({ row }) => <code title={row.original.slug}>{row.original.slug}</code>,
    },
    {
      id: 'members',
      accessorKey: 'memberCount',
      header: () => t('workspaces.columns.members'),
      cell: ({ row }) => (
        <span className={styles.numeric}>
          {new Intl.NumberFormat(locale).format(row.original.memberCount)}
        </span>
      ),
    },
    {
      id: 'created',
      accessorKey: 'createdAt',
      header: () => t('workspaces.columns.created'),
      cell: ({ row }) => (
        <span className={styles.numeric}>{formatEpoch(row.original.createdAt, locale) ?? '—'}</span>
      ),
    },
  ];

  return (
    <section className={styles.page}>
      <header className={styles.pageHeader}>
        <h1 className={styles.pageTitle}>{t('workspaces.title')}</h1>
        <Button variant="primary" onClick={handleCreate}>
          {t('workspaces.new')}
        </Button>
      </header>

      <DataGrid<Workspace>
        aria-label={t('workspaces.title')}
        columns={columns}
        data={workspaces}
        getRowId={(row) => row.id}
        emptyContent={
          <EmptyState
            titleAs="p"
            title={t('workspaces.empty')}
            action={
              <Button variant="default" onClick={handleCreate}>
                {t('workspaces.new')}
              </Button>
            }
          />
        }
        className={styles.grid}
      />

      <WorkspaceCreateDialog
        open={createOpen}
        onClose={() => {
          setCreateOpen(false);
        }}
      />
    </section>
  );
}
