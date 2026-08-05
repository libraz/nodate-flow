/**
 * PublicShareEmbedPage — chromeless variant of the public calendar share
 * purpose-built for `<iframe>` embedding at /embed/cal/{token}.
 *
 * It reuses the exact same data hook ({@link useShareRenderQuery}) and
 * read-only {@link ShareMonthGrid} as the branded `/share/cal` page, but
 * drops ALL chrome: no `PublicPageLayout`, no brand header, no cover, no
 * title block. The surface is edge-to-edge with a transparent background
 * (the grid root sets `data-embed`), so the host page's own styling shows
 * through and the embed blends into arbitrary sites. Theme tokens still
 * apply from the document root.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import ShareErrorView from './share-error-view';
import ShareMonthGrid from './share-month-grid';
import { useShareRenderQuery } from './use-share-render-query';

export interface PublicShareEmbedPageProps {
  token: string;
}

/** Common edge-to-edge frame for the chromeless embed surface. */
const embedRootStyle = {
  minBlockSize: '100vh',
  backgroundColor: 'transparent',
  padding: 'var(--nf-space-2)',
  boxSizing: 'border-box',
} as const;

export default function PublicShareEmbedPage({ token }: PublicShareEmbedPageProps): ReactElement {
  const { t } = useTranslation();
  const { data, isLoading, error } = useShareRenderQuery(token);

  if (isLoading) {
    return (
      <div style={embedRootStyle} aria-busy="true">
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Skeleton style={{ height: '24rem' }} />
      </div>
    );
  }

  if (error || !data) {
    return (
      <div style={embedRootStyle}>
        <ShareErrorView error={error} compact />
      </div>
    );
  }

  const { page, events } = data;
  const pageTimezone = page.timezone || 'UTC';

  return (
    <main style={embedRootStyle} aria-label={page.title}>
      {events.length === 0 ? (
        <p
          style={{
            padding: 'var(--nf-space-12) 0',
            textAlign: 'center',
            color: 'var(--nf-color-fg-subtle)',
          }}
        >
          {t('share.no_upcoming_events')}
        </p>
      ) : (
        <ShareMonthGrid events={events} timezone={pageTimezone} embed />
      )}
    </main>
  );
}
