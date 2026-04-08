/**
 * WorkspaceMembersTable — members tab content for a workspace detail.
 */

import Button from '@nodate-flow/ui/primitives/button';
import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import type { ColumnDef } from '@tanstack/react-table';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type WorkspaceMember, useWorkspaceMembersQuery } from './api';
import WorkspaceAddMemberDialog from './workspace-add-member-dialog';

export interface WorkspaceMembersTableProps {
  workspaceId: string;
}

function isZeroTime(iso: string): boolean {
  if (iso === '0001-01-01T00:00:00Z') return true;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) || d.getFullYear() < 2000;
}

function formatDate(iso: string | null | undefined, locale: string): string | null {
  if (!iso || isZeroTime(iso)) return null;
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(iso));
  } catch {
    return null;
  }
}

export default function WorkspaceMembersTable({
  workspaceId,
}: WorkspaceMembersTableProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: members } = useWorkspaceMembersQuery(workspaceId);
  const [addOpen, setAddOpen] = useState(false);
  const locale = i18n.resolvedLanguage ?? 'en';

  const columns: ColumnDef<WorkspaceMember, unknown>[] = [
    {
      id: 'email',
      accessorKey: 'email',
      header: () => t('workspaces.members.email'),
      cell: ({ row }) => <span>{row.original.email}</span>,
    },
    {
      id: 'role',
      accessorKey: 'role',
      header: () => t('workspaces.members.role'),
      cell: ({ row }) => <span>{row.original.role}</span>,
    },
    {
      id: 'added_at',
      header: () => t('workspaces.members.added_at'),
      cell: ({ row }) => {
        const formatted =
          formatDate(row.original.joinedAt, locale) ?? formatDate(row.original.createdAt, locale);
        return <span>{formatted ?? t('workspaces.members.pending')}</span>;
      },
    },
  ];

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h2
          style={{
            fontFamily: 'var(--font-display)',
            fontSize: '1.25rem',
            margin: 0,
          }}
        >
          {t('workspaces.members.title')}
        </h2>
        <Button
          variant="primary"
          size="sm"
          onClick={() => {
            setAddOpen(true);
          }}
        >
          {t('workspaces.members.add')}
        </Button>
      </header>

      <DataGrid<WorkspaceMember>
        aria-label={t('workspaces.members.title')}
        columns={columns}
        data={members}
        style={{ minBlockSize: '16rem' }}
      />

      <WorkspaceAddMemberDialog
        workspaceId={workspaceId}
        open={addOpen}
        onClose={() => {
          setAddOpen(false);
        }}
      />
    </section>
  );
}
