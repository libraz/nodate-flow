/**
 * WorkspaceInvitesList — table of active invite links for a workspace.
 *
 * Rendered below the members DataGrid in the Members tab.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Button from '@nodate-flow/ui/primitives/button';
import PresenceRow from '@nodate-flow/ui/primitives/presence-row';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { formatEpochDateTime } from '../../lib/format';
import ui from '../settings/settings-ui.module.css';
import { useListInvitesQuery, useRevokeInvite } from './invite-api';
import styles from './workspaces.module.css';

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
  // Invites present at first paint; one created afterwards fades in.
  const [initialIds] = useState<ReadonlySet<string>>(() => new Set(invites.map((i) => i.id)));
  const presence = usePresenceList(invites, (invite) => invite.id);
  const revokingId = revokeInvite.isPending ? revokeInvite.variables?.inviteId : undefined;
  const locale = i18n.resolvedLanguage ?? 'en';

  const handleRevoke = async (inviteId: string): Promise<void> => {
    const confirmed = await confirmAction({
      message: t('workspaces.invites.revoke_confirm'),
      confirmLabel: t('workspaces.invites.revoke'),
      tone: 'danger',
    });
    if (!confirmed) return;
    try {
      await revokeInvite.mutateAsync({ wsId: workspaceId, inviteId });
      toaster.show({ tone: 'success', message: t('workspaces.invites.revoked') });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspaces.invites.revoke_failed'),
      });
    }
  };

  if (presence.entries.length === 0) return null;

  return (
    <section className={styles.invitesSection}>
      <h3 className={styles.invitesTitle}>{t('workspaces.invites.list_title')}</h3>
      <div className={ui.tableWrap}>
        <table className={ui.table} aria-label={t('workspaces.invites.list_title')}>
          <thead>
            <tr>
              <th>{t('workspaces.invites.col_label')}</th>
              <th>{t('workspaces.invites.col_role')}</th>
              <th>{t('workspaces.invites.col_expires')}</th>
              <th>{t('workspaces.invites.col_uses')}</th>
              <th>{t('workspaces.invites.col_created_by')}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {presence.entries.map(({ key, item: invite, present }) => (
              <PresenceRow
                key={key}
                present={present}
                arrived={!initialIds.has(invite.id)}
                onExited={() => {
                  presence.onExited(key);
                }}
              >
                <td>{invite.label || t('workspaces.invites.untitled')}</td>
                <td>{invite.role}</td>
                <td>{formatExpiry(invite.expiresAt, locale, t)}</td>
                <td className={ui.numeric}>{formatUses(invite.useCount, invite.maxUses, t)}</td>
                <td>{invite.createdByName}</td>
                <td className={ui.alignEnd}>
                  <Button
                    variant="danger"
                    size="sm"
                    data-testid="invite-revoke"
                    disabled={revokeInvite.isPending}
                    aria-busy={revokingId === invite.id || undefined}
                    onClick={() => {
                      void handleRevoke(invite.id);
                    }}
                  >
                    {t('workspaces.invites.revoke')}
                  </Button>
                </td>
              </PresenceRow>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
