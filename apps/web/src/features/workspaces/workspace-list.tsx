/**
 * WorkspaceList — renders the caller's workspaces in a DataGrid.
 *
 * Rows link to /workspaces/$id. The header hosts a "New workspace" button
 * which opens the create dialog.
 */

import Button from '@nodate-flow/ui/primitives/button';
import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import { Link } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type Workspace, useWorkspacesQuery } from './api';
import WorkspaceCreateDialog from './workspace-create-dialog';

function formatDate(iso: string, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(iso));
  } catch {
    return iso;
  }
}

export default function WorkspaceList(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  const [createOpen, setCreateOpen] = useState(false);

  const locale = i18n.resolvedLanguage ?? 'en';

  const columns: ColumnDef<Workspace, unknown>[] = [
    {
      id: 'name',
      accessorKey: 'name',
      header: () => t('workspaces.columns.name'),
      cell: ({ row }) => (
        <Link
          to="/workspaces/$id"
          params={{ id: row.original.id }}
          style={{
            color: 'var(--color-fg)',
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
      header: () => t('workspaces.columns.slug'),
      cell: ({ row }) => <code>{row.original.slug}</code>,
    },
    {
      id: 'members',
      accessorKey: 'memberCount',
      header: () => t('workspaces.columns.members'),
      cell: ({ row }) => (
        <span>{new Intl.NumberFormat(locale).format(row.original.memberCount)}</span>
      ),
    },
    {
      id: 'created',
      accessorKey: 'createdAt',
      header: () => t('workspaces.columns.created'),
      cell: ({ row }) => <span>{formatDate(row.original.createdAt, locale)}</span>,
    },
  ];

  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
        blockSize: '100%',
      }}
    >
      <header
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: '1rem',
        }}
      >
        <h1
          style={{
            fontFamily: 'var(--font-display)',
            fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
            margin: 0,
          }}
        >
          {t('workspaces.title')}
        </h1>
        <Button
          variant="primary"
          onClick={() => {
            setCreateOpen(true);
          }}
        >
          {t('workspaces.new')}
        </Button>
      </header>

      <DataGrid<Workspace>
        aria-label={t('workspaces.title')}
        columns={columns}
        data={workspaces}
        emptyContent={t('workspaces.empty')}
        style={{ minBlockSize: '20rem' }}
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
