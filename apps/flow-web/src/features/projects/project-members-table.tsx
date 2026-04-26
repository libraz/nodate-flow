/**
 * ProjectMembersTable — members tab content for a project detail.
 */

import Button from '@nodate-flow/ui/primitives/button';
import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import type { ColumnDef } from '@tanstack/react-table';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatEpoch } from '../../lib/format';
import { type ProjectMember, useProjectMembersQuery } from './api';
import ProjectAddMemberDialog from './project-add-member-dialog';

export interface ProjectMembersTableProps {
  projectId: string;
}

export default function ProjectMembersTable({ projectId }: ProjectMembersTableProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: members } = useProjectMembersQuery(projectId);
  const [addOpen, setAddOpen] = useState(false);
  const locale = i18n.resolvedLanguage ?? 'en';

  const roleLabel = (r: string): string => {
    switch (r) {
      case 'lead':
        return t('projects.roles.lead');
      case 'editor':
        return t('projects.roles.editor');
      case 'commenter':
        return t('projects.roles.commenter');
      case 'viewer':
        return t('projects.roles.viewer');
      default:
        return r;
    }
  };

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
      cell: ({ row }) => <span>{roleLabel(row.original.role)}</span>,
    },
    {
      id: 'added_at',
      header: () => t('projects.members.added_at'),
      cell: ({ row }) => (
        <span>{formatEpoch(row.original.addedAt ?? row.original.createdAt, locale) ?? '—'}</span>
      ),
    },
  ];

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h2
          style={{
            fontFamily: 'var(--font-display)',
            fontSize: 'var(--nf-text-xl)',
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
        emptyContent={
          <span
            style={{
              display: 'block',
              padding: '2rem 1rem',
              textAlign: 'center',
              color: 'var(--nf-color-fg-muted)',
              border: '1px dashed var(--nf-color-border)',
              borderRadius: '0.5rem',
              background: 'var(--nf-color-bg-sunken, transparent)',
            }}
          >
            {t('projects.members.empty')}
          </span>
        }
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
