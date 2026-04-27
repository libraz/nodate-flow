/**
 * AvatarUpload — profile-picture section for the {@code /settings/profile}
 * page.
 *
 * Renders a circular preview (current image, optimistic local preview
 * while uploading, or styled initials placeholder when empty) plus a
 * Change / Upload / Remove action row. The preview itself is a button
 * so both the image and the buttons open the OS file picker.
 *
 * Mutations hit the dedicated avatar endpoints (not {@code PATCH /me})
 * and on success sync two caches:
 *   1. the react-query {@code ['me']} slot (so the Profile page sees the
 *      new picture immediately on re-render);
 *   2. the Zustand {@code authStore.user.avatarUrl} (so the top-bar
 *      avatar — which reads the store, not the suspense query — flips
 *      to the new image without reload).
 */

import Button from '@nodate-flow/ui/primitives/button';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Camera } from 'lucide-react';
import { type ChangeEvent, type ReactElement, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type ApiError, formatApiError } from '../../lib/api-error';
import { authStore } from '../auth/auth-store';
import { type Me, settingsKeys } from './api';
import { deleteAvatar, uploadAvatar } from './avatar-api';
import styles from './avatar-upload.module.css';

/**
 * Extract up to two-letter initials from a display name (same logic
 * used by the top-bar trigger — kept private per-file to avoid
 * exporting a trivial helper from a public barrel).
 */
function initialsFrom(name: string | undefined): string {
  if (!name) return '?';
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return '?';
  const first = parts[0]?.[0] ?? '';
  const last = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : '';
  return (first + last).toUpperCase() || '?';
}

/**
 * Push the updated avatar URL into the Zustand auth store so the
 * top-bar trigger (which reads the store, not the suspense query)
 * picks up the change without a page reload.
 */
function syncAuthStoreAvatar(avatarUrl: string | null | undefined): void {
  const { accessToken, user, setSession } = authStore.getState();
  if (!accessToken || !user) return;
  setSession(accessToken, { ...user, avatarUrl: avatarUrl ?? null });
}

export interface AvatarUploadProps {
  user: Me;
}

/** Default export; rendered from ProfileForm above the text fields. */
export default function AvatarUpload({ user }: AvatarUploadProps): ReactElement {
  const { t } = useTranslation(['settings', 'errors']);
  const inputRef = useRef<HTMLInputElement>(null);
  const [localPreview, setLocalPreview] = useState<string | null>(null);
  const [imgError, setImgError] = useState(false);
  const qc = useQueryClient();
  const descriptionId = useId();

  // Revoke the object URL when the component unmounts or when we
  // swap previews — avoids leaking blobs into the page lifetime.
  useEffect(() => {
    return () => {
      if (localPreview) URL.revokeObjectURL(localPreview);
    };
  }, [localPreview]);

  const uploadMut = useMutation<Me, ApiError, File>({
    throwOnError: false,
    mutationFn: (file) => uploadAvatar(file),
    onSuccess: (data) => {
      qc.setQueryData<Me>(settingsKeys.me, data);
      void qc.invalidateQueries({ queryKey: settingsKeys.me });
      syncAuthStoreAvatar(data.avatarUrl);
      setImgError(false);
      toaster.show({ tone: 'success', message: t('settings:profile.avatar.updated') });
    },
    onError: (err) => {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'settings:profile.avatar.upload_error'),
      });
    },
    onSettled: () => {
      // Drop the optimistic blob URL regardless of success/failure —
      // on success the server URL takes over, on failure we revert to
      // whatever the user had before.
      setLocalPreview((prev) => {
        if (prev) URL.revokeObjectURL(prev);
        return null;
      });
    },
  });

  const removeMut = useMutation<Me, ApiError, void>({
    throwOnError: false,
    mutationFn: () => deleteAvatar(),
    onSuccess: (data) => {
      qc.setQueryData<Me>(settingsKeys.me, data);
      void qc.invalidateQueries({ queryKey: settingsKeys.me });
      syncAuthStoreAvatar(null);
      setImgError(false);
      toaster.show({ tone: 'success', message: t('settings:profile.avatar.removed') });
    },
    onError: (err) => {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'settings:profile.avatar.remove_error'),
      });
    },
  });

  const busy = uploadMut.isPending || removeMut.isPending;
  const hasRemoteAvatar = Boolean(user.avatarUrl) && !imgError;
  const previewSrc = localPreview ?? (hasRemoteAvatar ? (user.avatarUrl ?? null) : null);
  const initials = initialsFrom(user.displayName);
  const altText = previewSrc
    ? t('settings:profile.avatar.alt', { name: user.displayName })
    : t('settings:profile.avatar.empty_alt');
  const hasImage = Boolean(previewSrc);

  const handleFileSelected = (event: ChangeEvent<HTMLInputElement>): void => {
    const file = event.target.files?.[0];
    // Reset the input so picking the same file twice in a row still fires.
    event.target.value = '';
    if (!file) return;
    // Show the picked file immediately; the server round-trip will
    // swap to the canonical proxy URL on success.
    const url = URL.createObjectURL(file);
    setLocalPreview((prev) => {
      if (prev) URL.revokeObjectURL(prev);
      return url;
    });
    setImgError(false);
    uploadMut.mutate(file);
  };

  const openPicker = (): void => {
    if (busy) return;
    inputRef.current?.click();
  };

  const primaryLabel = hasImage
    ? t('settings:profile.avatar.change')
    : t('settings:profile.avatar.upload');
  const primaryInFlight = uploadMut.isPending
    ? t('settings:profile.avatar.uploading')
    : primaryLabel;
  // The circular preview is an icon-only file-picker trigger (it
  // composites an avatar/initials with a centered Camera glyph), so
  // its accessible name needs to read fully on its own — the visible
  // sibling button is the textual fallback for sighted users.
  const previewAriaLabel = hasImage
    ? t('settings:profile.avatar.replace_aria')
    : t('settings:profile.avatar.upload_aria');

  return (
    <section className={styles.root} aria-describedby={descriptionId}>
      <div className={styles.header}>
        <h2 className={styles.label}>{t('settings:profile.avatar.label')}</h2>
        <p id={descriptionId} className={styles.description}>
          {t('settings:profile.avatar.description')}
        </p>
      </div>

      <div className={styles.previewRow}>
        <button
          type="button"
          className={`${styles.preview} nf-focus-ring`}
          onClick={openPicker}
          disabled={busy}
          aria-label={previewAriaLabel}
        >
          {previewSrc ? (
            <img
              className={styles.previewImage}
              src={previewSrc}
              alt={altText}
              onError={() => {
                // Fall back to initials if the remote proxy 404s or
                // the external OIDC URL becomes unreachable.
                setImgError(true);
              }}
            />
          ) : (
            <span className={styles.previewInitials} aria-hidden="true">
              {initials}
            </span>
          )}
          <span className={styles.overlay} aria-hidden="true">
            <Camera className={styles.overlayIcon} />
          </span>
          {busy ? (
            <span className={styles.busy}>
              <Spinner size="md" label={t('settings:profile.avatar.uploading')} />
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
            {primaryInFlight}
          </Button>
          {hasRemoteAvatar ? (
            <Button
              type="button"
              variant="ghost"
              onClick={() => removeMut.mutate()}
              disabled={busy}
            >
              {t('settings:profile.avatar.remove')}
            </Button>
          ) : null}
        </div>
      </div>

      <input
        ref={inputRef}
        type="file"
        accept="image/jpeg,image/png,image/webp,image/gif"
        className={styles.hiddenInput}
        onChange={handleFileSelected}
        tabIndex={-1}
        aria-hidden="true"
      />
    </section>
  );
}
