/**
 * WorkspaceInvitesList — table of active invite links for a workspace.
 *
 * Rendered below the members DataGrid in the Members tab.
 */

import Button from '@nodate-flow/ui/primitives/button';
import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import type { ColumnDef } from '@tanstack/react-table';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { formatEpochDateTime } from '../../lib/format';
import { useListInvitesQuery, useRevokeInvite, type WorkspaceInvite } from './invite-api';

export interface WorkspaceInvitesListProps {
  workspaceId: string;
}

function formatExpiry(
  epoch: number | undefined,
  locale: string,
  t: (key: string) => string,
): string {
  if (!epoch) return t('workspaces.invites.never');
  return formatEpochDateTime(epoch, locale) ?? t('workspaces.invites.never');
}

function formatUses(useCount: number, maxUses: number | null, t: (key: string) => string): string {
  if (maxUses == null) return `${useCount} / ${t('workspaces.invites.unlimited')}`;
  return `${useCount} / ${maxUses}`;
}

export default function WorkspaceInvitesList({
  workspaceId,
}: WorkspaceInvitesListProps): ReactElement | null {
  const { t, i18n } = useTranslation('common');
  const { data: invites } = useListInvitesQuery(workspaceId);
  const revokeInvite = useRevokeInvite();
  const locale = i18n.resolvedLanguage ?? 'en';

  const handleRevoke = async (inviteId: string): Promise<void> => {
    try {
      await revokeInvite.mutateAsync({ wsId: workspaceId, inviteId });
      toaster.show({ tone: 'success', message: t('workspaces.invites.revoked') });
    } catch {
      toaster.show({ tone: 'danger', message: t('workspaces.invites.revoke_failed') });
    }
  };

  if (invites.length === 0) return null;

  const columns: ColumnDef<WorkspaceInvite, unknown>[] = [
    {
      id: 'label',
      accessorKey: 'label',
      header: () => t('workspaces.invites.col_label'),
      cell: ({ row }) => <span>{row.original.label || t('workspaces.invites.untitled')}</span>,
    },
    {
      id: 'role',
      accessorKey: 'role',
      header: () => t('workspaces.invites.col_role'),
      cell: ({ row }) => <span>{row.original.role}</span>,
    },
    {
      id: 'expires',
      header: () => t('workspaces.invites.col_expires'),
      cell: ({ row }) => <span>{formatExpiry(row.original.expiresAt, locale, t)}</span>,
    },
    {
      id: 'uses',
      header: () => t('workspaces.invites.col_uses'),
      cell: ({ row }) => <span>{formatUses(row.original.useCount, row.original.maxUses, t)}</span>,
    },
    {
      id: 'createdBy',
      accessorKey: 'createdByName',
      header: () => t('workspaces.invites.col_created_by'),
      cell: ({ row }) => <span>{row.original.createdByName}</span>,
    },
    {
      id: 'actions',
      header: () => '',
      cell: ({ row }) => (
        <Button
          variant="danger"
          size="sm"
          data-testid="invite-revoke"
          disabled={revokeInvite.isPending}
          onClick={() => {
            void handleRevoke(row.original.id);
          }}
        >
          {t('workspaces.invites.revoke')}
        </Button>
      ),
    },
  ];

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}>
      <h3
        style={{
          fontFamily: 'var(--nf-font-display)',
          fontSize: 'var(--nf-text-base)',
          margin: 0,
        }}
      >
        {t('workspaces.invites.list_title')}
      </h3>
      <DataGrid<WorkspaceInvite>
        aria-label={t('workspaces.invites.list_title')}
        columns={columns}
        data={invites}
        // nf-token-override: component dimension, not a spacing step
        style={{ minBlockSize: '8rem' }}
      />
    </section>
  );
}
