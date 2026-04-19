/**
 * WorkspaceMembersTable — members tab content for a workspace detail.
 *
 * Includes the members DataGrid with role-change and remove actions,
 * a button to create invite links, and the active invites list.
 */

import Button from '@nodate-flow/ui/primitives/button';
import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import type { ColumnDef } from '@tanstack/react-table';
import { type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatEpoch } from '../../lib/format';
import { selectUser, useAuth } from '../auth/auth-store';
import {
  type WorkspaceMember,
  useRemoveMember,
  useUpdateMemberRole,
  useWorkspaceMembersQuery,
} from './api';
import WorkspaceAddMemberDialog from './workspace-add-member-dialog';
import WorkspaceInviteDialog from './workspace-invite-dialog';
import WorkspaceInvitesList from './workspace-invites-list';

export interface WorkspaceMembersTableProps {
  workspaceId: string;
}

type MemberRole = 'owner' | 'admin' | 'member' | 'guest';
const ROLES: readonly MemberRole[] = ['owner', 'admin', 'member', 'guest'];

export default function WorkspaceMembersTable({
  workspaceId,
}: WorkspaceMembersTableProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: members } = useWorkspaceMembersQuery(workspaceId);
  const currentUser = useAuth(selectUser);
  const updateRole = useUpdateMemberRole();
  const removeMember = useRemoveMember();
  const [addOpen, setAddOpen] = useState(false);
  const [inviteOpen, setInviteOpen] = useState(false);
  const locale = i18n.resolvedLanguage ?? 'en';

  // Determine if the current user is an admin/owner in this workspace
  const currentMember = members.find((m) => m.userId === currentUser?.id);
  const isAdmin = currentMember?.role === 'admin' || currentMember?.role === 'owner';

  const handleRoleChange = async (userId: string, role: MemberRole): Promise<void> => {
    try {
      await updateRole.mutateAsync({ wsId: workspaceId, userId, role });
      toaster.show({ tone: 'success', message: t('workspaces.members.role_updated') });
    } catch {
      toaster.show({ tone: 'danger', message: t('workspaces.members.role_update_failed') });
    }
  };

  const handleRemove = async (userId: string): Promise<void> => {
    try {
      await removeMember.mutateAsync({ wsId: workspaceId, userId });
      toaster.show({ tone: 'success', message: t('workspaces.members.removed') });
    } catch {
      toaster.show({ tone: 'danger', message: t('workspaces.members.remove_failed') });
    }
  };

  const roleLabel = (r: string): string => {
    switch (r) {
      case 'owner':
        return t('workspaces.roles.owner');
      case 'admin':
        return t('workspaces.roles.admin');
      case 'member':
        return t('workspaces.roles.member');
      case 'guest':
        return t('workspaces.roles.guest');
      default:
        return r;
    }
  };

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
      cell: ({ row }) => {
        const member = row.original;
        const isSelf = member.userId === currentUser?.id;
        if (!isAdmin || isSelf || member.role === 'owner') {
          return <span>{roleLabel(member.role)}</span>;
        }
        return (
          <Select
            value={member.role}
            onChange={(e) => {
              void handleRoleChange(member.userId, e.target.value as MemberRole);
            }}
            aria-label={t('workspaces.members.change_role')}
            style={{ minInlineSize: '7rem' }}
          >
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {roleLabel(r)}
              </option>
            ))}
          </Select>
        );
      },
    },
    {
      id: 'added_at',
      header: () => t('workspaces.members.added_at'),
      cell: ({ row }) => {
        const formatted =
          formatEpoch(row.original.joinedAt, locale) ?? formatEpoch(row.original.createdAt, locale);
        return <span>{formatted ?? t('workspaces.members.pending')}</span>;
      },
    },
    ...(isAdmin
      ? [
          {
            id: 'actions',
            header: () => '',
            cell: ({ row }: { row: { original: WorkspaceMember } }) => {
              const member = row.original;
              const isSelf = member.userId === currentUser?.id;
              if (isSelf || member.role === 'owner') return null;
              return (
                <Button
                  variant="danger"
                  size="sm"
                  disabled={removeMember.isPending}
                  onClick={() => {
                    void handleRemove(member.userId);
                  }}
                >
                  {t('workspaces.members.remove')}
                </Button>
              );
            },
          } satisfies ColumnDef<WorkspaceMember, unknown>,
        ]
      : []),
  ];

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem' }}>
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
        <div style={{ display: 'flex', gap: '0.5rem' }}>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setInviteOpen(true);
            }}
          >
            {t('workspaces.invites.create')}
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() => {
              setAddOpen(true);
            }}
          >
            {t('workspaces.members.add')}
          </Button>
        </div>
      </header>

      <DataGrid<WorkspaceMember>
        aria-label={t('workspaces.members.title')}
        columns={columns}
        data={members}
        style={{ minBlockSize: '16rem' }}
      />

      <Suspense fallback={null}>
        <WorkspaceInvitesList workspaceId={workspaceId} />
      </Suspense>

      <WorkspaceAddMemberDialog
        workspaceId={workspaceId}
        open={addOpen}
        onClose={() => {
          setAddOpen(false);
        }}
      />

      <WorkspaceInviteDialog
        workspaceId={workspaceId}
        open={inviteOpen}
        onClose={() => {
          setInviteOpen(false);
        }}
      />
    </section>
  );
}
