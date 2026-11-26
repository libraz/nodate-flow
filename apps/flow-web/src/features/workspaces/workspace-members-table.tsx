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
import { Link2, UserMinus, UserPlus } from 'lucide-react';
import { type ReactElement, Suspense, useState } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import MemberIdentity from '../../components/member-identity';
import PageHelp from '../../components/page-help';
import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { formatEpoch } from '../../lib/format';
import { selectUser, useAuth } from '../auth/auth-store';
import {
  useRemoveMember,
  useUpdateMemberRole,
  useWorkspaceMembersQuery,
  type WorkspaceMember,
} from './api';
import WorkspaceAddMemberDialog from './workspace-add-member-dialog';
import WorkspaceInviteDialog from './workspace-invite-dialog';
import WorkspaceInvitesList from './workspace-invites-list';
import styles from './workspaces.module.css';

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
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspaces.members.role_update_failed'),
      });
    }
  };

  const handleRemove = async (member: WorkspaceMember): Promise<void> => {
    const confirmed = await confirmAction({
      message: t('workspaces.members.remove_confirm', { name: member.displayName }),
      tone: 'danger',
    });
    if (!confirmed) return;
    const { userId } = member;
    try {
      await removeMember.mutateAsync({ wsId: workspaceId, userId });
      toaster.show({ tone: 'success', message: t('workspaces.members.removed') });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspaces.members.remove_failed'),
      });
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
      id: 'member',
      accessorKey: 'displayName',
      header: () => t('workspaces.members.member'),
      cell: ({ row }) => (
        <MemberIdentity displayName={row.original.displayName} email={row.original.email} />
      ),
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
            // nf-token-override: component dimension, not a spacing step
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
        return (
          <span style={formatted ? { fontVariantNumeric: 'tabular-nums' } : undefined}>
            {formatted ?? t('workspaces.members.pending')}
          </span>
        );
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
              // Only the row being removed waits; the others stay actionable.
              const removing =
                removeMember.isPending && removeMember.variables?.userId === member.userId;
              return (
                <Button
                  variant="ghost"
                  size="sm"
                  className={styles.removeButton}
                  disabled={removing}
                  aria-busy={removing || undefined}
                  onClick={() => {
                    void handleRemove(member);
                  }}
                >
                  <UserMinus size={14} aria-hidden />
                  {t('workspaces.members.remove')}
                </Button>
              );
            },
          } satisfies ColumnDef<WorkspaceMember, unknown>,
        ]
      : []),
  ];

  return (
    <section className={styles.membersSection}>
      <header className={styles.membersHeader}>
        <div className={styles.membersTitleRow}>
          <h2 className={styles.membersTitle}>{t('workspaces.members.title')}</h2>
          <PageHelp topic="workspace-members" />
        </div>
        {isAdmin ? (
          <div className={styles.membersActions}>
            <Button
              variant="default"
              data-testid="invite-create-open"
              onClick={() => {
                setInviteOpen(true);
              }}
            >
              <Link2 size={16} aria-hidden />
              {t('workspaces.invites.create')}
            </Button>
            <Button
              variant="primary"
              onClick={() => {
                setAddOpen(true);
              }}
            >
              <UserPlus size={16} aria-hidden />
              {t('workspaces.members.add')}
            </Button>
          </div>
        ) : null}
      </header>

      <DataGrid<WorkspaceMember>
        aria-label={t('workspaces.members.title')}
        columns={columns}
        data={members}
        getRowId={(row) => row.userId}
        // nf-token-override: component dimension, not a spacing step
        style={{ minBlockSize: '16rem' }}
      />

      {/* The invite list is admin-only; other roles would only be refused. */}
      {isAdmin ? (
        <ErrorBoundary fallback={null}>
          <Suspense fallback={null}>
            <WorkspaceInvitesList workspaceId={workspaceId} />
          </Suspense>
        </ErrorBoundary>
      ) : null}

      <WorkspaceAddMemberDialog
        workspaceId={workspaceId}
        canGrantOwner={currentMember?.role === 'owner'}
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
