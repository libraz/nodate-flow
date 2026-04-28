/**
 * ShareList — table of public share pages for a workspace, with a
 * create button. Suspense-ready via `usePublicSharesQuery`. Delete
 * and rotate-token actions are provided inline; adding/removing events
 * from a share is handled elsewhere (event editor).
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link } from '@tanstack/react-router';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import { formatEpochDateTime } from '../../lib/format';
import { getPublicBaseUrl } from '../../lib/public-base-url';
import {
  type PublicShare,
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
  const [createOpen, setCreateOpen] = useState(false);
  const [rotatedShare, setRotatedShare] = useState<PublicShare | null>(null);
  const locale = i18n.resolvedLanguage ?? 'en';

  const handleDelete = async (share: PublicShare): Promise<void> => {
    if (
      !(await confirmAction({
        message: t('workspace.public_shares.delete_confirm', { title: share.title }),
      }))
    ) {
      return;
    }
    try {
      await deleteShare.mutateAsync(share.id);
      toaster.show({ tone: 'success', message: t('workspace.public_shares.deleted') });
    } catch {
      toaster.show({
        tone: 'danger',
        message: t('workspace.public_shares.errors.delete_failed'),
      });
    }
  };

  const handleRotate = async (share: PublicShare): Promise<void> => {
    if (!(await confirmAction({ message: t('workspace.public_shares.rotate_confirm') }))) return;
    try {
      const result = await rotate.mutateAsync(share.id);
      setRotatedShare(result);
    } catch {
      toaster.show({
        tone: 'danger',
        message: t('workspace.public_shares.errors.rotate_failed'),
      });
    }
  };

  return (
    <section className={styles.section}>
      <header className={styles.header}>
        <div className={styles.headerIdentity}>
          <h1 className={styles.title}>{t('workspace.public_shares.title')}</h1>
          <p className={styles.description}>{t('workspace.public_shares.description')}</p>
        </div>
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
      </header>

      {shares.length === 0 ? (
        <p className={styles.empty}>{t('workspace.public_shares.empty')}</p>
      ) : (
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr className={styles.headerRow}>
                <th className={styles.headerCell}>{t('workspace.public_shares.table.title')}</th>
                <th className={styles.headerCell}>{t('workspace.public_shares.table.events')}</th>
                <th className={styles.headerCell}>
                  {t('workspace.public_shares.table.expires_at')}
                </th>
                <th className={styles.headerCell}>
                  {t('workspace.public_shares.table.created_at')}
                </th>
                <th className={styles.headerCellEnd}>
                  {t('workspace.public_shares.table.actions')}
                </th>
              </tr>
            </thead>
            <tbody>
              {shares.map((share) => (
                <tr key={share.id} className={styles.row}>
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
                  <td className={styles.cell}>
                    {formatEpochDateTime(share.expiresAt, locale) ??
                      t('workspace.public_shares.never_expires')}
                  </td>
                  <td className={styles.cell}>
                    {formatEpochDateTime(share.createdAt, locale) ?? '—'}
                  </td>
                  <td className={styles.cellActions}>
                    <Button
                      type="button"
                      variant="ghost"
                      data-testid="public-share-rotate"
                      onClick={() => {
                        void handleRotate(share);
                      }}
                    >
                      {t('workspace.public_shares.rotate')}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      data-testid="public-share-delete"
                      onClick={() => {
                        void handleDelete(share);
                      }}
                    >
                      {t('workspace.public_shares.delete')}
                    </Button>
                  </td>
                </tr>
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
