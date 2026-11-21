/**
 * AvatarUpload -- profile-picture section of the profile page.
 *
 * Circular preview (server image, optimistic local preview while
 * uploading, or initials) plus Change / Remove actions. Both the preview and
 * the button open the file picker. A successful call rebuilds the auth-store
 * session from the returned `/me` body so the shell avatar updates without a
 * reload.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { confirmAction } from '@nodate-flow/ui/primitives/confirm/action';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Camera } from 'lucide-react';
import { type ChangeEvent, type ReactElement, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { refusalCode } from '../../lib/auth-errors';
import { authStore } from '../auth/auth-store';
import { userFromMe } from '../auth/user-from-me';
import { deleteAvatar, type Me, uploadAvatar } from './avatar-api';
import styles from './avatar-upload.module.css';
import { resolveAvatarUrl } from './avatar-url';

function initialsFrom(name: string | undefined): string {
  if (!name) return '?';
  const parts = name.trim().split(/\s+/).filter(Boolean);
  const first = parts[0]?.[0] ?? '';
  const last = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : '';
  return (first + last).toUpperCase() || '?';
}

function syncAuthStore(me: Me): void {
  const { accessToken, setSession } = authStore.getState();
  if (!accessToken) return;
  setSession(accessToken, userFromMe(me));
}

export interface AvatarUploadProps {
  avatarUrl: string | null | undefined;
  displayName: string | undefined;
}

export default function AvatarUpload({ avatarUrl, displayName }: AvatarUploadProps): ReactElement {
  const { t } = useTranslation('auth');
  const inputRef = useRef<HTMLInputElement>(null);
  const [localPreview, setLocalPreview] = useState<string | null>(null);
  const [imgError, setImgError] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [removing, setRemoving] = useState(false);
  const descriptionId = useId();

  // Release the blob URL when it is swapped out or the section unmounts.
  useEffect(() => {
    return () => {
      if (localPreview) URL.revokeObjectURL(localPreview);
    };
  }, [localPreview]);

  const busy = uploading || removing;
  const remotePreview = resolveAvatarUrl(avatarUrl);
  const hasRemoteAvatar = Boolean(remotePreview) && !imgError;
  const previewSrc = localPreview ?? (hasRemoteAvatar ? remotePreview : null);
  const hasImage = Boolean(previewSrc);

  const showFailure = (
    messageKey: 'profile.avatar.upload_error' | 'profile.avatar.remove_error',
    err: unknown,
  ): void => {
    const code = refusalCode(err);
    toaster.show({
      tone: 'danger',
      message: code ? `${t(messageKey)} (${code})` : t(messageKey),
    });
  };

  const handleFileSelected = async (event: ChangeEvent<HTMLInputElement>): Promise<void> => {
    const file = event.target.files?.[0];
    // Reset so picking the same file twice in a row still fires.
    event.target.value = '';
    if (!file) return;
    setLocalPreview(URL.createObjectURL(file));
    setImgError(false);
    setUploading(true);
    try {
      syncAuthStore(await uploadAvatar(file));
      toaster.show({ tone: 'success', message: t('profile.avatar.updated') });
    } catch (err) {
      showFailure('profile.avatar.upload_error', err);
    } finally {
      setUploading(false);
      setLocalPreview(null);
    }
  };

  const openPicker = (): void => {
    if (busy) return;
    inputRef.current?.click();
  };

  const handleRemove = async (): Promise<void> => {
    const confirmed = await confirmAction({
      message: t('profile.avatar.remove_confirm'),
      confirmLabel: t('profile.avatar.remove'),
      tone: 'danger',
    });
    if (!confirmed) return;
    setRemoving(true);
    try {
      syncAuthStore(await deleteAvatar());
      setImgError(false);
      toaster.show({ tone: 'success', message: t('profile.avatar.removed') });
    } catch (err) {
      showFailure('profile.avatar.remove_error', err);
    } finally {
      setRemoving(false);
    }
  };

  const primaryLabel = uploading
    ? t('profile.avatar.uploading')
    : hasImage
      ? t('profile.avatar.change')
      : t('profile.avatar.upload');

  return (
    <section className={styles.root} aria-labelledby={`${descriptionId}-label`}>
      <div className={styles.header}>
        <h3 id={`${descriptionId}-label`} className={styles.label}>
          {t('profile.avatar.label')}
        </h3>
        <p id={descriptionId} className={styles.description}>
          {t('profile.avatar.description')}
        </p>
      </div>

      <div className={styles.previewRow}>
        <button
          type="button"
          className={`${styles.preview} nf-focus-ring`}
          onClick={openPicker}
          disabled={busy}
          aria-label={hasImage ? t('profile.avatar.replace_aria') : t('profile.avatar.upload_aria')}
        >
          {previewSrc ? (
            <img
              className={styles.previewImage}
              src={previewSrc}
              alt={t('profile.avatar.alt', { name: displayName ?? '' })}
              onError={() => {
                setImgError(true);
              }}
            />
          ) : (
            <span className={styles.previewInitials} aria-hidden="true">
              {initialsFrom(displayName)}
            </span>
          )}
          <span className={styles.overlay} aria-hidden="true">
            <Camera className={styles.overlayIcon} />
          </span>
          {busy ? (
            <span className={styles.busy}>
              <Spinner size="md" label={t('profile.avatar.uploading')} />
            </span>
          ) : null}
        </button>

        <div className={styles.actions}>
          <Button
            type="button"
            variant={hasImage ? 'default' : 'primary'}
            onClick={openPicker}
            disabled={busy}
          >
            {primaryLabel}
          </Button>
          {hasRemoteAvatar ? (
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                void handleRemove();
              }}
              disabled={busy}
              aria-busy={removing || undefined}
            >
              {t('profile.avatar.remove')}
            </Button>
          ) : null}
        </div>
      </div>

      <input
        ref={inputRef}
        type="file"
        accept="image/jpeg,image/png,image/webp,image/gif"
        className={styles.hiddenInput}
        onChange={(e) => {
          void handleFileSelected(e);
        }}
        tabIndex={-1}
        aria-hidden="true"
      />
    </section>
  );
}
