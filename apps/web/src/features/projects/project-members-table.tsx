/**
 * ProjectMembersTable — members tab content for a project detail.
 */

import Button from '@nodate-flow/ui/primitives/button';
import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import type { ColumnDef } from '@tanstack/react-table';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type ProjectMember, useProjectMembersQuery } from './api';
import ProjectAddMemberDialog from './project-add-member-dialog';

export interface ProjectMembersTableProps {
  projectId: string;
}

function formatDate(iso: string | undefined, locale: string): string {
  if (!iso) return '';
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(iso));
  } catch {
    return iso;
  }
}

export default function ProjectMembersTable({ projectId }: ProjectMembersTableProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: members } = useProjectMembersQuery(projectId);
  const [addOpen, setAddOpen] = useState(false);
  const locale = i18n.resolvedLanguage ?? 'en';

  const columns: ColumnDef<ProjectMember, unknown>[] = [
    {
      id: 'email',
      accessorKey: 'email',
      header: () => t('projects.members.email'),
      cell: ({ row }) => <span>{row.original.email}</span>,
    },
    {
      id: 'role',
      accessorKey: 'role',
      header: () => t('projects.members.role'),
      cell: ({ row }) => <span>{row.original.role}</span>,
    },
    {
      id: 'added_at',
      header: () => t('projects.members.added_at'),
      cell: ({ row }) => (
        <span>{formatDate(row.original.addedAt ?? row.original.createdAt, locale)}</span>
      ),
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
          {t('projects.members.title')}
        </h2>
        <Button
          variant="primary"
          size="sm"
          onClick={() => {
            setAddOpen(true);
          }}
        >
          {t('projects.members.add')}
        </Button>
      </header>

      <DataGrid<ProjectMember>
        aria-label={t('projects.members.title')}
        columns={columns}
        data={members}
        style={{ minBlockSize: '16rem' }}
      />

      <ProjectAddMemberDialog
        projectId={projectId}
        open={addOpen}
        onClose={() => {
          setAddOpen(false);
        }}
      />
    </section>
  );
}
