/**
 * ExpirySection — shows a share's expiry on the detail page as text and
 * lets the user change it or remove it. Server refusals go through
 * `formatApiError`.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { CalendarClock, CalendarX } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { formatEpochDateTime } from '../../lib/format';
import { type PublicShare, usePatchPublicShare } from './api';
import ExpiryField, { draftFromEpoch, draftToEpoch } from './expiry-field';
import styles from './expiry-section.module.css';

export interface ExpirySectionProps {
  workspaceId: string;
  share: Pick<PublicShare, 'id' | 'expiresAt'>;
}

export default function ExpirySection({ workspaceId, share }: ExpirySectionProps): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const locale = i18n.resolvedLanguage ?? 'en';
  const patch = usePatchPublicShare(workspaceId);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(() => draftFromEpoch(share.expiresAt));
  const [error, setError] = useState('');

  const when = formatEpochDateTime(share.expiresAt, locale);
  const expired = share.expiresAt != null && share.expiresAt * 1000 <= Date.now();

  const startEditing = (): void => {
    setDraft(draftFromEpoch(share.expiresAt));
    setError('');
    setEditing(true);
  };

  const handleSave = async (): Promise<void> => {
    const expiresAt = draftToEpoch(draft);
    if (expiresAt === undefined) {
      await handleClear();
      return;
    }
    if (!(expiresAt > Math.floor(Date.now() / 1000))) {
      setError(t('workspace.public_shares.expiry.error_past'));
      return;
    }
    try {
      await patch.mutateAsync({ shareId: share.id, patch: { expiresAt } });
      setEditing(false);
      toaster.show({ tone: 'success', message: t('workspace.public_shares.expiry.saved') });
    } catch (err) {
      setError(formatApiError(err, t, 'workspace.public_shares.expiry.save_failed'));
    }
  };

  const handleClear = async (): Promise<void> => {
    try {
      await patch.mutateAsync({ shareId: share.id, patch: { clearExpiresAt: true } });
      setEditing(false);
      toaster.show({ tone: 'success', message: t('workspace.public_shares.expiry.cleared') });
    } catch (err) {
      setError(formatApiError(err, t, 'workspace.public_shares.expiry.save_failed'));
    }
  };

  return (
    <section className={styles.section} aria-labelledby="share-expiry-heading">
      <div className={styles.summary}>
        <div>
          <h2 id="share-expiry-heading" className={styles.heading}>
            {t('workspace.public_shares.expiry.label')}
          </h2>
          <p
            className={expired ? styles.statusExpired : styles.status}
            data-testid="share-expiry-status"
          >
            {expired ? (
              <CalendarX size={14} aria-hidden />
            ) : (
              <CalendarClock size={14} aria-hidden />
            )}
            {when === null
              ? t('workspace.public_shares.expiry.status_never')
              : expired
                ? t('workspace.public_shares.expiry.status_expired', { when })
                : t('workspace.public_shares.expiry.status_expires', { when })}
          </p>
        </div>
        {editing ? null : (
          <Button type="button" onClick={startEditing} data-testid="share-expiry-edit">
            {t('workspace.public_shares.expiry.change')}
          </Button>
        )}
      </div>

      {editing ? (
        <div className={styles.editor}>
          <ExpiryField
            draft={draft}
            onChange={(next) => {
              setDraft(next);
              setError('');
            }}
            error={error === '' ? undefined : error}
          />
          <div className={styles.actions}>
            {share.expiresAt != null ? (
              <Button
                type="button"
                variant="danger"
                disabled={patch.isPending}
                onClick={() => {
                  void handleClear();
                }}
                data-testid="share-expiry-clear"
              >
                {t('workspace.public_shares.expiry.clear')}
              </Button>
            ) : null}
            <span className={styles.spacer} />
            <Button
              type="button"
              variant="ghost"
              disabled={patch.isPending}
              onClick={() => {
                setEditing(false);
              }}
            >
              {t('workspace.public_shares.dialog.cancel')}
            </Button>
            <Button
              type="button"
              variant="primary"
              disabled={patch.isPending}
              aria-busy={patch.isPending || undefined}
              onClick={() => {
                void handleSave();
              }}
              data-testid="share-expiry-save"
            >
              {t('workspace.public_shares.expiry.save')}
            </Button>
          </div>
        </div>
      ) : null}
    </section>
  );
}
