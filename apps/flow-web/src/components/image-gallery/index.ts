import { lazy } from 'react';

/** Gallery chunk: the lightbox library and its CSS load on first open only. */
export const LazyImageGallery = lazy(() => import('./image-gallery'));

export type {
  AttachmentThumbnailProps,
  AttachmentThumbnailStatus,
} from './attachment-thumbnail';
export { AttachmentFileSlot, default as AttachmentThumbnail } from './attachment-thumbnail';
export type { GallerySlide, ImageGalleryProps } from './image-gallery';
export { GALLERY_REFRESH_MS, MAX_AUTO_PREVIEW_BYTES } from './preview-limits';
