/**
 * ShareLensDialog — modal for publishing/unpublishing a lens as a public
 * shareable link.
 *
 * When the lens is private, shows a publish button with a confirmation
 * message. When public, shows the shareable URL with a copy button and
 * an unpublish option.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { getPublicBaseUrl } from '../../lib/public-base-url';
import { usePublishLens, useUnpublishLens } from './api';
import styles from './sharing.module.css';

export interface ShareLensDialogProps {
  workspaceId: string;
  lensId: string;
  /** Current public token, if the lens is already published. */
  publicToken: string | null;
  open: boolean;
  onClose: () => void;
  /** Called after publish/unpublish so the parent can refresh lens state. */
  onTokenChange?: (token: string | null) => void;
}

function buildPublicUrl(token: string): string {
  return `${getPublicBaseUrl()}/public/lenses/${token}`;
}

export default function ShareLensDialog({
  workspaceId,
  lensId,
  publicToken,
  open,
  onClose,
  onTokenChange,
}: ShareLensDialogProps): ReactElement {
  const { t } = useTranslation('sharing');
  const { t: tc } = useTranslation('common');
  const publishMutation = usePublishLens(workspaceId);
  const unpublishMutation = useUnpublishLens(workspaceId);
  const linkRef = useRef<HTMLInputElement>(null);

  const [currentToken, setCurrentToken] = useState(publicToken);
  const isPublic = currentToken !== null;
  const isPending = publishMutation.isPending || unpublishMutation.isPending;

  const handleClose = (): void => {
    if (isPending) return;
    onClose();
  };

  const handlePublish = async (): Promise<void> => {
    try {
      const result = await publishMutation.mutateAsync({ lensId });
      setCurrentToken(result.publicToken);
      onTokenChange?.(result.publicToken);
      toaster.show({ tone: 'success', message: t('toast.published') });
    } catch {
      toaster.show({ tone: 'danger', message: t('error.publish_failed') });
    }
  };

  const handleUnpublish = async (): Promise<void> => {
    try {
      await unpublishMutation.mutateAsync({ lensId });
      setCurrentToken(null);
      onTokenChange?.(null);
      toaster.show({ tone: 'success', message: t('toast.unpublished') });
    } catch {
      toaster.show({ tone: 'danger', message: t('error.unpublish_failed') });
    }
  };

  const handleCopyLink = async (): Promise<void> => {
    if (!currentToken) return;
    const url = buildPublicUrl(currentToken);
    try {
      await navigator.clipboard.writeText(url);
      toaster.show({ tone: 'success', message: t('link_copied') });
      linkRef.current?.select();
    } catch {
      // Fallback: select the text so the user can copy manually
      linkRef.current?.select();
    }
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('title')}>
      <div className={styles.dialog}>
        {/* Status badge */}
        <div className={styles.statusSection}>
          <span
            className={`${styles.statusBadge} ${isPublic ? styles.statusPublic : styles.statusPrivate}`}
          >
            {isPublic ? t('badge') : t('badge_private')}
          </span>
        </div>

        {isPublic && currentToken ? (
          <>
            {/* Public link display */}
            <div className={styles.linkSection}>
              <span className={styles.linkLabel}>{t('public_link')}</span>
              <div className={styles.linkRow}>
                <input
                  ref={linkRef}
                  type="text"
                  className={`${styles.linkInput} nf-focus-ring`}
                  value={buildPublicUrl(currentToken)}
                  readOnly
                  aria-label={t('public_link')}
                />
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => {
                    void handleCopyLink();
                  }}
                >
                  {t('copy_link')}
                </Button>
              </div>
            </div>

            {/* Unpublish */}
            <p className={styles.confirmText}>{t('confirm_unpublish')}</p>
            <div className={styles.actions}>
              <Button type="button" variant="ghost" onClick={handleClose} disabled={isPending}>
                {tc('actions.cancel')}
              </Button>
              <Button
                type="button"
                variant="danger"
                onClick={() => {
                  void handleUnpublish();
                }}
                disabled={isPending}
              >
                {t('unpublish')}
              </Button>
            </div>
          </>
        ) : (
          <>
            {/* Publish confirmation */}
            <p className={styles.confirmText}>{t('confirm_publish')}</p>
            <div className={styles.actions}>
              <Button type="button" variant="ghost" onClick={handleClose} disabled={isPending}>
                {tc('actions.cancel')}
              </Button>
              <Button
                type="button"
                variant="primary"
                onClick={() => {
                  void handlePublish();
                }}
                disabled={isPending}
              >
                {t('publish')}
              </Button>
            </div>
          </>
        )}
      </div>
    </Dialog>
  );
}
