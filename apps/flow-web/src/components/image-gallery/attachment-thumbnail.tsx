/**
 * AttachmentThumbnail — 56px preview chip at the start of an attachment row.
 *
 * Presentational only: the owning feature decides the status and supplies the
 * URL. `AttachmentFileSlot` is the inert box used for non-image rows and for
 * previews that could not be loaded.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { FileText, Image as ImageIcon } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import styles from './attachment-thumbnail.module.css';

export type AttachmentThumbnailStatus = 'loading' | 'ready' | 'unavailable' | 'oversized';

export interface AttachmentThumbnailProps {
  filename: string;
  status: AttachmentThumbnailStatus;
  src?: string;
  onImageError: () => void;
  onOpen: (button: HTMLButtonElement) => void;
}

/** Inert slot with a generic file icon; decorative, not a focus stop. */
export function AttachmentFileSlot(): ReactElement {
  return (
    <span className={styles.slot} aria-hidden="true">
      <FileText className={styles.icon} size={20} strokeWidth={1.75} />
    </span>
  );
}

/** Thumbnail button (or inert slot when the preview is unavailable). */
export default function AttachmentThumbnail({
  filename,
  status,
  src,
  onImageError,
  onOpen,
}: AttachmentThumbnailProps): ReactElement {
  const { t } = useTranslation('common');
  const [loadedSrc, setLoadedSrc] = useState<string | null>(null);

  if (status === 'unavailable') return <AttachmentFileSlot />;

  return (
    <button
      type="button"
      className={`${styles.slot} ${styles.button}`}
      aria-label={t('gallery.open_preview', { filename })}
      title={status === 'oversized' ? t('gallery.too_large_hint') : undefined}
      data-status={status}
      onClick={(e) => onOpen(e.currentTarget)}
    >
      {status === 'loading' ? <Skeleton className={styles.fill} /> : null}
      {status === 'oversized' ? (
        <ImageIcon className={styles.icon} size={20} strokeWidth={1.75} aria-hidden />
      ) : null}
      {status === 'ready' && src ? (
        <img
          className={styles.image}
          src={src}
          alt=""
          width={56}
          height={56}
          loading="lazy"
          decoding="async"
          data-loaded={loadedSrc === src ? '' : undefined}
          onLoad={() => setLoadedSrc(src)}
          onError={onImageError}
        />
      ) : null}
    </button>
  );
}
