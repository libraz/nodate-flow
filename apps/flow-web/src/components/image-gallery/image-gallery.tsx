/**
 * ImageGallery — full-screen image viewer built on yet-another-react-lightbox.
 *
 * This is the only module that imports the library and its CSS, so consumers
 * load it through `LazyImageGallery`. The portal resolves tokens against the
 * dark sibling of the active theme, so the viewer stays dark in every theme.
 * Mounted only while open; the parent unmounts it from `onClose`.
 */

import Lightbox, { type Labels } from 'yet-another-react-lightbox';
import Captions from 'yet-another-react-lightbox/plugins/captions';
import Counter from 'yet-another-react-lightbox/plugins/counter';
import Download from 'yet-another-react-lightbox/plugins/download';
import Fullscreen from 'yet-another-react-lightbox/plugins/fullscreen';
import Rotate from 'yet-another-react-lightbox/plugins/rotate';
import Slideshow from 'yet-another-react-lightbox/plugins/slideshow';
import Thumbnails from 'yet-another-react-lightbox/plugins/thumbnails';
import Zoom from 'yet-another-react-lightbox/plugins/zoom';
import 'yet-another-react-lightbox/styles.css';
import 'yet-another-react-lightbox/plugins/captions.css';
import 'yet-another-react-lightbox/plugins/counter.css';
import 'yet-another-react-lightbox/plugins/thumbnails.css';
import type { ReactElement } from 'react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import styles from './image-gallery.module.css';

declare module 'yet-another-react-lightbox' {
  interface GenericSlide {
    /** Attachment id the slide was built from; used to route downloads. */
    attachmentId?: string;
  }
}

export interface GallerySlide {
  id: string;
  src: string;
  filename: string;
}

export interface ImageGalleryProps {
  slides: GallerySlide[];
  index: number;
  onClose: () => void;
  onDownload: (id: string) => void;
  onExited: () => void;
}

type UsedLabelKey =
  | 'Previous'
  | 'Next'
  | 'Close'
  | 'Slide'
  | 'Carousel'
  | 'Lightbox'
  | 'Photo gallery'
  | '{index} of {total}'
  | 'Zoom in'
  | 'Zoom out'
  | 'Rotate clockwise'
  | 'Rotate counterclockwise'
  | 'Enter Fullscreen'
  | 'Exit Fullscreen'
  | 'Play'
  | 'Pause'
  | 'Download'
  | 'Thumbnails'
  | 'Show thumbnails'
  | 'Hide thumbnails'
  | 'Caption'
  | 'Show captions'
  | 'Hide captions';

/** `<family>-light` maps to `<family>-dark`; a dark id is kept; anything else yields none. */
function resolveDarkTheme(theme: string | null): string | undefined {
  if (!theme) return undefined;
  if (theme.endsWith('-dark')) return theme;
  if (theme.endsWith('-light')) return `${theme.slice(0, -'-light'.length)}-dark`;
  return undefined;
}

/** Image viewer with zoom, rotate, thumbnails, full screen, slideshow, captions and download. */
export default function ImageGallery({
  slides,
  index,
  onClose,
  onDownload,
  onExited,
}: ImageGalleryProps): ReactElement {
  const { t } = useTranslation('common');
  const [darkTheme] = useState(() =>
    resolveDarkTheme(document.documentElement.getAttribute('data-theme')),
  );
  const [reduceMotion] = useState(
    () => window.matchMedia('(prefers-reduced-motion: reduce)').matches,
  );

  // biome-ignore-start lint/style/useNamingConvention: keys are YARL's label identifiers
  const labels = {
    Previous: t('gallery.labels.previous'),
    Next: t('gallery.labels.next'),
    Close: t('gallery.labels.close'),
    Slide: t('gallery.labels.slide'),
    Carousel: t('gallery.labels.carousel'),
    Lightbox: t('gallery.labels.lightbox'),
    'Photo gallery': t('gallery.labels.photo_gallery'),
    // YARL substitutes the placeholders itself, so they must survive ICU formatting.
    '{index} of {total}': t('gallery.labels.index_of_total', {
      index: '{index}',
      total: '{total}',
    }),
    'Zoom in': t('gallery.labels.zoom_in'),
    'Zoom out': t('gallery.labels.zoom_out'),
    'Rotate clockwise': t('gallery.labels.rotate_cw'),
    'Rotate counterclockwise': t('gallery.labels.rotate_ccw'),
    'Enter Fullscreen': t('gallery.labels.enter_fullscreen'),
    'Exit Fullscreen': t('gallery.labels.exit_fullscreen'),
    Play: t('gallery.labels.play'),
    Pause: t('gallery.labels.pause'),
    Download: t('gallery.labels.download'),
    Thumbnails: t('gallery.labels.thumbnails'),
    'Show thumbnails': t('gallery.labels.show_thumbnails'),
    'Hide thumbnails': t('gallery.labels.hide_thumbnails'),
    Caption: t('gallery.labels.caption'),
    'Show captions': t('gallery.labels.show_captions'),
    'Hide captions': t('gallery.labels.hide_captions'),
  } satisfies Required<Pick<Labels, UsedLabelKey>>;
  // biome-ignore-end lint/style/useNamingConvention: keys are YARL's label identifiers

  const single = slides.length <= 1;
  const themeAttr: { 'data-theme'?: string } = darkTheme ? { 'data-theme': darkTheme } : {};

  return (
    <Lightbox
      open
      index={index}
      close={onClose}
      slides={slides.map((s) => ({
        src: s.src,
        alt: s.filename,
        title: s.filename,
        attachmentId: s.id,
      }))}
      labels={labels}
      plugins={
        single
          ? [Zoom, Rotate, Fullscreen, Slideshow, Captions, Download]
          : [Zoom, Rotate, Fullscreen, Slideshow, Thumbnails, Captions, Counter, Download]
      }
      portal={{ container: { className: styles.root, ...themeAttr } }}
      carousel={{ preload: 1, finite: single }}
      zoom={{ maxZoomPixelRatio: 4, scrollToZoom: true }}
      controller={{ closeOnBackdropClick: true }}
      slideshow={{ delay: 4000 }}
      download={{
        download: ({ slide }) => {
          if (slide.attachmentId) onDownload(slide.attachmentId);
        },
      }}
      on={{ exited: onExited }}
      {...(single ? { render: { buttonPrev: () => null, buttonNext: () => null } } : {})}
      {...(reduceMotion ? { animation: { fade: 0, swipe: 0, zoom: 0 } } : {})}
    />
  );
}
