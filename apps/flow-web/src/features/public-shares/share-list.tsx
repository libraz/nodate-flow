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
import {
  type PublicShare,
  useDeletePublicShare,
  usePublicSharesQuery,
  useRotatePublicShareToken,
} from './api';
import PublicShareCreateDialog from './create-dialog';
import RotateTokenDialog from './rotate-dialog';

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
    <section style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
      <header
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          gap: '1rem',
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
          <h1 style={{ margin: 0, fontSize: '1.5rem' }}>{t('workspace.public_shares.title')}</h1>
          <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.875rem' }}>
            {t('workspace.public_shares.description')}
          </p>
        </div>
        <Button
          type="button"
          variant="primary"
          onClick={() => {
            setCreateOpen(true);
          }}
        >
          {t('workspace.public_shares.create')}
        </Button>
      </header>

      {shares.length === 0 ? (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
          {t('workspace.public_shares.empty')}
        </p>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ inlineSize: '100%', borderCollapse: 'collapse', fontSize: '0.875rem' }}>
            <thead>
              <tr style={{ textAlign: 'start', color: 'var(--nf-color-fg-muted)' }}>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                  {t('workspace.public_shares.table.title')}
                </th>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                  {t('workspace.public_shares.table.events')}
                </th>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                  {t('workspace.public_shares.table.expires_at')}
                </th>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                  {t('workspace.public_shares.table.created_at')}
                </th>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'end' }}>
                  {t('workspace.public_shares.table.actions')}
                </th>
              </tr>
            </thead>
            <tbody>
              {shares.map((share) => (
                <tr key={share.id} style={{ borderBlockStart: '1px solid var(--nf-color-border)' }}>
                  <td style={{ padding: '0.75rem' }}>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.125rem' }}>
                      <Link
                        to="/workspaces/$id/settings/public-shares/$shareId"
                        params={{ id: workspaceId, shareId: share.id }}
                        style={{
                          fontWeight: 500,
                          color: 'var(--nf-color-fg)',
                          textDecoration: 'none',
                        }}
                      >
                        {share.title}
                      </Link>
                      {share.description ? (
                        <span
                          style={{
                            fontSize: '0.75rem',
                            color: 'var(--nf-color-fg-muted)',
                          }}
                        >
                          {share.description}
                        </span>
                      ) : null}
                    </div>
                  </td>
                  <td style={{ padding: '0.75rem' }}>{share.eventCount}</td>
                  <td style={{ padding: '0.75rem' }}>
                    {formatEpochDateTime(share.expiresAt, locale) ??
                      t('workspace.public_shares.never_expires')}
                  </td>
                  <td style={{ padding: '0.75rem' }}>
                    {formatEpochDateTime(share.createdAt, locale) ?? '—'}
                  </td>
                  <td
                    style={{
                      padding: '0.75rem',
                      textAlign: 'end',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    <Button
                      type="button"
                      variant="ghost"
                      onClick={() => {
                        void handleRotate(share);
                      }}
                    >
                      {t('workspace.public_shares.rotate')}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
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
        webUrl={window.location.origin}
        onClose={() => {
          setRotatedShare(null);
        }}
      />
    </section>
  );
}
