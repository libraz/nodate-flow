/**
 * ProjectMembersTable — members tab content for a project detail.
 *
 * The remove action mirrors WorkspaceMembersTable: the column only exists
 * for a caller whose own row grants the privilege, it is derived from the
 * member list already on hand rather than a second query, and it refuses
 * the two rows whose removal would strand the project — yourself and the
 * last remaining lead.
 */

import Button from '@nodate-flow/ui/primitives/button';
import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import type { ColumnDef } from '@tanstack/react-table';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatEpoch } from '../../lib/format';
import { selectUser, useAuth } from '../auth/auth-store';
import { type ProjectMember, useProjectMembersQuery, useRemoveProjectMember } from './api';
import ProjectAddMemberDialog from './project-add-member-dialog';

export interface ProjectMembersTableProps {
  projectId: string;
}

export default function ProjectMembersTable({ projectId }: ProjectMembersTableProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: members } = useProjectMembersQuery(projectId);
  const currentUser = useAuth(selectUser);
  const removeMember = useRemoveProjectMember();
  const [addOpen, setAddOpen] = useState(false);
  const locale = i18n.resolvedLanguage ?? 'en';

  // `lead` is the project's admin role — the one the add/remove endpoints
  // are documented to require.
  const currentMember = members.find((m) => m.userId === currentUser?.id);
  const isLead = currentMember?.role === 'lead';
  const leadCount = members.filter((m) => m.role === 'lead').length;

  const handleRemove = async (member: ProjectMember): Promise<void> => {
    try {
      await removeMember.mutateAsync({ id: projectId, userId: member.userId });
      toaster.show({ tone: 'success', message: t('projects.members.removed') });
    } catch {
      toaster.show({ tone: 'danger', message: t('projects.members.remove_failed') });
    }
  };

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
    ...(isLead
      ? [
          {
            id: 'actions',
            header: () => '',
            cell: ({ row }: { row: { original: ProjectMember } }) => {
              const member = row.original;
              const isSelf = member.userId === currentUser?.id;
              // Removing the last lead would leave the project with
              // nobody able to manage its membership, which the endpoint
              // refuses anyway — so do not offer it as a button.
              const isLastLead = member.role === 'lead' && leadCount <= 1;
              if (isSelf || isLastLead) return null;
              return (
                <Button
                  variant="danger"
                  size="sm"
                  disabled={removeMember.isPending}
                  onClick={() => {
                    void handleRemove(member);
                  }}
                >
                  {t('projects.members.remove')}
                </Button>
              );
            },
          } satisfies ColumnDef<ProjectMember, unknown>,
        ]
      : []),
  ];

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h2
          style={{
            fontFamily: 'var(--nf-font-display)',
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
              padding: 'var(--nf-space-8) var(--nf-space-4)',
              textAlign: 'center',
              color: 'var(--nf-color-fg-muted)',
              border: '1px dashed var(--nf-color-border)',
              borderRadius: 'var(--nf-radius-md)',
              background: 'var(--nf-color-bg-sunken)',
            }}
          >
            {t('projects.members.empty')}
          </span>
        }
        // nf-token-override: component dimension, not a spacing step
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
