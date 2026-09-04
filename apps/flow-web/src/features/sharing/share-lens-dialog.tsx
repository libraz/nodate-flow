/**
 * ShareLensDialog — modal for publishing/unpublishing a lens as a public
 * shareable link.
 *
 * Whether the lens is public and whether this session holds the share link
 * are two different questions, so the dialog takes both `isPublic` and
 * `publicToken` and has three states:
 *
 *   1. private — a publish confirmation.
 *   2. public, token in hand — the link, a copy button, and unpublish. The
 *      API returns the plaintext token only in the publish response, so this
 *      is reachable only right after publishing in this session.
 *   3. public, token not in hand — the state is reported and the link is
 *      declared unavailable rather than fabricated. Publishing is not
 *      offered: the API refuses it on an already-public lens.
 *
 * Publishing and unpublishing are gated by the API to the lens creator and
 * to workspace admins / owners; `canManage` says whether this viewer is one
 * of them. Without it the dialog is read-only: it still reports whether a
 * share URL is live and lets the viewer copy the link when it is at hand,
 * because that link is already public, but it offers no control that would
 * be refused.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { getPublicBaseUrl } from '../../lib/public-base-url';
import { usePublishLens, useUnpublishLens } from './api';
import styles from './sharing.module.css';

export interface ShareLensDialogProps {
  workspaceId: string;
  lensId: string;
  /** Whether the lens is currently published, per the lens record. */
  isPublic: boolean;
  /**
   * The plaintext share token, when this session happens to hold it — the
   * API hands it out once, in the publish response, and stores only its
   * digest. A published lens whose token was minted in an earlier session
   * arrives here as `null`.
   */
  publicToken: string | null;
  /**
   * Whether the viewer may publish or unpublish this lens: its creator, or
   * a workspace admin / owner. When false the dialog shows the current
   * state without offering either action.
   */
  canManage: boolean;
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
  isPublic,
  publicToken,
  canManage,
  open,
  onClose,
  onTokenChange,
}: ShareLensDialogProps): ReactElement {
  const { t } = useTranslation('sharing');
  const publishMutation = usePublishLens(workspaceId);
  const unpublishMutation = useUnpublishLens(workspaceId);
  const linkRef = useRef<HTMLInputElement>(null);

  // Publishing and unpublishing inside the dialog move both facts, so they
  // are held locally from the props the dialog mounted with. Holding a token
  // implies the lens is public even if the caller's copy of the row has not
  // refreshed yet.
  const [currentToken, setCurrentToken] = useState(publicToken);
  const [published, setPublished] = useState(isPublic || publicToken !== null);
  const isPending = publishMutation.isPending || unpublishMutation.isPending;

  const handleClose = (): void => {
    if (isPending) return;
    onClose();
  };

  const handlePublish = async (): Promise<void> => {
    try {
      const result = await publishMutation.mutateAsync({ lensId });
      setCurrentToken(result.publicToken);
      setPublished(true);
      onTokenChange?.(result.publicToken);
      toaster.show({ tone: 'success', message: t('toast.published') });
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, 'error.publish_failed') });
    }
  };

  const handleUnpublish = async (): Promise<void> => {
    try {
      await unpublishMutation.mutateAsync({ lensId });
      setCurrentToken(null);
      setPublished(false);
      onTokenChange?.(null);
      toaster.show({ tone: 'success', message: t('toast.unpublished') });
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, 'error.unpublish_failed') });
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
            className={`${styles.statusBadge} ${published ? styles.statusPublic : styles.statusPrivate}`}
          >
            {published ? t('badge') : t('badge_private')}
          </span>
        </div>

        {published ? (
          <>
            {currentToken ? (
              /* Public link display */
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
            ) : (
              /*
               * The link exists but this session cannot show it: the API
               * returned the plaintext token once and kept only its digest.
               * Nothing resembling a URL is rendered here on purpose.
               */
              <div className={styles.linkSection}>
                <p className={styles.confirmText}>{t('link_unavailable')}</p>
                <p className={styles.warningText}>{t('link_unavailable_hint')}</p>
              </div>
            )}

            {/* Unpublish */}
            {canManage ? (
              <>
                <p className={styles.confirmText}>{t('confirm_unpublish')}</p>
                <div className={styles.actions}>
                  <Button type="button" variant="ghost" onClick={handleClose} disabled={isPending}>
                    {t('cancel')}
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
              <div className={styles.actions}>
                <Button type="button" variant="ghost" onClick={handleClose}>
                  {t('close')}
                </Button>
              </div>
            )}
          </>
        ) : canManage ? (
          <>
            {/* Publish confirmation */}
            <p className={styles.confirmText}>{t('confirm_publish')}</p>
            <div className={styles.actions}>
              <Button type="button" variant="ghost" onClick={handleClose} disabled={isPending}>
                {t('cancel')}
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
        ) : (
          <div className={styles.actions}>
            <Button type="button" variant="ghost" onClick={handleClose}>
              {t('close')}
            </Button>
          </div>
        )}
      </div>
    </Dialog>
  );
}
