/**
 * ShareList — table of public share pages for a workspace, with a
 * create button. Suspense-ready via `usePublicSharesQuery`. Delete
 * and rotate-token actions are provided inline; adding/removing events
 * from a share is handled elsewhere (event editor).
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import PresenceRow from '@nodate-flow/ui/primitives/presence-row';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { Link } from '@tanstack/react-router';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import PageHeader from '../../components/page-header';
import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { formatEpochDateTime } from '../../lib/format';
import { getPublicBaseUrl } from '../../lib/public-base-url';
import ui from '../settings/settings-ui.module.css';
import {
  type PublicShare,
  type PublicShareWithToken,
  useDeletePublicShare,
  usePublicSharesQuery,
  useRotatePublicShareToken,
} from './api';
import PublicShareCreateDialog from './create-dialog';
import RotateTokenDialog from './rotate-dialog';
import styles from './share-list.module.css';

export interface ShareListProps {
  workspaceId: string;
}

export default function ShareList({ workspaceId }: ShareListProps): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const { data: shares } = usePublicSharesQuery(workspaceId);
  const deleteShare = useDeletePublicShare(workspaceId);
  const rotate = useRotatePublicShareToken(workspaceId);
  const { entries, onExited } = usePresenceList(shares, (share) => share.id);
  const [createOpen, setCreateOpen] = useState(false);
  const [rotatedShare, setRotatedShare] = useState<PublicShareWithToken | null>(null);
  const locale = i18n.resolvedLanguage ?? 'en';
  // The column appears only once some share carries an expiry.
  const showExpiry = shares.some((share) => share.expiresAt != null);

  const handleDelete = async (share: PublicShare): Promise<void> => {
    if (
      !(await confirmAction({
        message: t('workspace.public_shares.delete_confirm', { title: share.title }),
        confirmLabel: t('workspace.public_shares.delete'),
      }))
    ) {
      return;
    }
    try {
      await deleteShare.mutateAsync(share.id);
      toaster.show({ tone: 'success', message: t('workspace.public_shares.deleted') });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspace.public_shares.errors.delete_failed'),
      });
    }
  };

  const handleRotate = async (share: PublicShare): Promise<void> => {
    if (
      !(await confirmAction({
        message: t('workspace.public_shares.rotate_confirm'),
        confirmLabel: t('workspace.public_shares.rotate'),
      }))
    )
      return;
    try {
      const result = await rotate.mutateAsync(share.id);
      setRotatedShare(result);
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspace.public_shares.errors.rotate_failed'),
      });
    }
  };

  return (
    <section className={styles.section}>
      <PageHeader
        title={t('workspace.public_shares.title')}
        description={t('workspace.public_shares.description')}
        help="workspace-settings-public-shares"
        actions={
          <Button
            type="button"
            variant="primary"
            data-testid="public-share-create-open"
            onClick={() => {
              setCreateOpen(true);
            }}
          >
            {t('workspace.public_shares.create')}
          </Button>
        }
      />

      {entries.length === 0 ? (
        <EmptyState
          titleAs="p"
          title={t('workspace.public_shares.empty')}
          className={ui.empty}
          action={
            <Button
              type="button"
              onClick={() => {
                setCreateOpen(true);
              }}
            >
              {t('workspace.public_shares.create')}
            </Button>
          }
        />
      ) : (
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr className={styles.headerRow}>
                <th className={styles.headerCell}>{t('workspace.public_shares.table.title')}</th>
                <th className={styles.headerCell}>{t('workspace.public_shares.table.events')}</th>
                {showExpiry ? (
                  <th className={styles.headerCell}>
                    {t('workspace.public_shares.table.expires_at')}
                  </th>
                ) : null}
                <th className={styles.headerCell}>
                  {t('workspace.public_shares.table.created_at')}
                </th>
                <th className={styles.headerCellEnd}>
                  {t('workspace.public_shares.table.actions')}
                </th>
              </tr>
            </thead>
            <tbody>
              {entries.map(({ key, item: share, present }) => (
                <PresenceRow
                  key={key}
                  present={present}
                  onExited={() => onExited(key)}
                  className={styles.row}
                >
                  <td className={styles.cell}>
                    <div className={styles.titleCellInner}>
                      <Link
                        to="/workspaces/$id/settings/public-shares/$shareId"
                        params={{ id: workspaceId, shareId: share.id }}
                        className={styles.titleLink}
                      >
                        {share.title}
                      </Link>
                      {share.description ? (
                        <span className={styles.shareDescription}>{share.description}</span>
                      ) : null}
                    </div>
                  </td>
                  <td className={styles.cell}>{share.eventCount}</td>
                  {showExpiry ? (
                    <td className={styles.cell}>
                      {formatEpochDateTime(share.expiresAt, locale) ??
                        t('workspace.public_shares.never_expires')}
                      {share.expiresAt != null && share.expiresAt * 1000 <= Date.now()
                        ? ` · ${t('workspace.public_shares.expiry.expired_tag')}`
                        : null}
                    </td>
                  ) : null}
                  <td className={styles.cell}>
                    {formatEpochDateTime(share.createdAt, locale) ?? '—'}
                  </td>
                  <td className={styles.cellActions}>
                    <Tooltip content={t('workspace.public_shares.rotate_hint')}>
                      <Button
                        type="button"
                        size="sm"
                        data-testid="public-share-rotate"
                        disabled={rotate.isPending}
                        aria-busy={(rotate.isPending && rotate.variables === share.id) || undefined}
                        onClick={() => {
                          void handleRotate(share);
                        }}
                      >
                        {t('workspace.public_shares.rotate')}
                      </Button>
                    </Tooltip>
                    <Button
                      type="button"
                      size="sm"
                      variant="danger"
                      data-testid="public-share-delete"
                      disabled={deleteShare.isPending}
                      aria-busy={
                        (deleteShare.isPending && deleteShare.variables === share.id) || undefined
                      }
                      onClick={() => {
                        void handleDelete(share);
                      }}
                    >
                      {t('workspace.public_shares.delete')}
                    </Button>
                  </td>
                </PresenceRow>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <PublicShareCreateDialog
        workspaceId={workspaceId}
        open={createOpen}
        onClose={() => {
          setCreateOpen(false);
        }}
      />
      <RotateTokenDialog
        share={rotatedShare}
        webUrl={getPublicBaseUrl()}
        onClose={() => {
          setRotatedShare(null);
        }}
      />
    </section>
  );
}
