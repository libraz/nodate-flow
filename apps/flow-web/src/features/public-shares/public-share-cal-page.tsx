/**
 * PublicShareCalPage — standalone read-only page that renders a public
 * calendar share by URL token. Accessible at /share/cal/{token} without
 * authentication; no sidebar, topbar, or navigation chrome.
 *
 * Backed by GET /share/cal/{token}. Events render as a read-only MONTH
 * GRID (see {@link ShareMonthGrid}) with month navigation, inside the
 * shared `PublicPageLayout` (brand header / cover / title block intact).
 * The page surfaces its own invalid/expired error states rather than
 * escalating to the root FatalFallback so anonymous visitors land on a
 * branded retry page.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { Calendar as CalendarIcon, Globe } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import PublicPageLayout from '../../components/public-page-layout';
import ShareErrorView from './share-error-view';
import ShareMonthGrid from './share-month-grid';
import { useShareRenderQuery } from './use-share-render-query';

export interface PublicShareCalPageProps {
  token: string;
}

export default function PublicShareCalPage({ token }: PublicShareCalPageProps): ReactElement {
  const { t, i18n } = useTranslation();
  const { data, isLoading, error } = useShareRenderQuery(token);

  if (isLoading) {
    return (
      <PublicPageLayout busy>
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Skeleton style={{ height: '3rem' }} />
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Skeleton style={{ height: '24rem' }} />
      </PublicPageLayout>
    );
  }

  if (error || !data) {
    return (
      <PublicPageLayout showBrandHeader alignMain="center">
        <ShareErrorView error={error} />
      </PublicPageLayout>
    );
  }

  const { page, events } = data;
  const pageTimezone = page.timezone || 'UTC';

  // Resolve the holidays country code (e.g. "JP") to a locale-aware display
  // name (e.g. "Japan") via Intl.DisplayNames, mirroring the authenticated
  // holidays surface; fall back to the raw code if resolution fails.
  let holidaysCountryLabel = page.showHolidaysCountry ?? '';
  if (page.showHolidaysCountry) {
    try {
      const displayNames = new Intl.DisplayNames([i18n.language], { type: 'region' });
      holidaysCountryLabel = displayNames.of(page.showHolidaysCountry) ?? page.showHolidaysCountry;
    } catch {
      holidaysCountryLabel = page.showHolidaysCountry;
    }
  }

  const titleHeader = (
    <header
      style={{
        maxInlineSize: 'var(--nf-measure-content)',
        inlineSize: '100%',
        marginInline: 'auto',
        padding: 'var(--nf-space-6) var(--nf-space-4) var(--nf-space-4)',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-2)',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-3)' }}>
        {page.iconUrl ? (
          <img
            src={page.iconUrl}
            alt=""
            style={{
              // nf-token-override: component dimension, not a spacing step
              width: '2.5rem',
              // nf-token-override: component dimension, not a spacing step
              height: '2.5rem',
              borderRadius: 'var(--nf-radius-md)',
              objectFit: 'cover',
            }}
          />
        ) : (
          <div
            style={{
              // nf-token-override: component dimension, not a spacing step
              width: '2.5rem',
              // nf-token-override: component dimension, not a spacing step
              height: '2.5rem',
              borderRadius: 'var(--nf-radius-md)',
              backgroundColor: 'var(--nf-color-accent-subtle)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
            aria-hidden="true"
          >
            <CalendarIcon size={20} style={{ color: 'var(--nf-color-accent-fg)' }} />
          </div>
        )}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-0-5)' }}>
          <h1
            style={{
              fontSize: 'var(--nf-text-xl)',
              fontWeight: 'var(--nf-weight-semibold)',
              color: 'var(--nf-color-fg)',
              margin: 0,
            }}
          >
            {page.title}
          </h1>
          <p
            style={{
              fontSize: 'var(--nf-text-sm)',
              color: 'var(--nf-color-fg-muted)',
              margin: 0,
            }}
          >
            {page.workspaceName}
          </p>
        </div>
      </div>

      {page.description ? (
        <p
          style={{
            fontSize: 'var(--nf-text-sm)',
            color: 'var(--nf-color-fg-muted)',
            whiteSpace: 'pre-wrap',
            margin: 0,
          }}
        >
          {page.description}
        </p>
      ) : null}

      <div
        style={{
          display: 'flex',
          flexWrap: 'wrap',
          gap: 'var(--nf-space-3)',
          fontSize: 'var(--nf-text-xs)',
          color: 'var(--nf-color-fg-subtle)',
        }}
      >
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--nf-space-1)' }}>
          <Globe size={12} />
          {pageTimezone}
        </span>
        {page.showHolidaysCountry ? (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--nf-space-1)' }}>
            {t('share.holidays_country', { country: holidaysCountryLabel })}
          </span>
        ) : null}
      </div>
    </header>
  );

  return (
    <PublicPageLayout
      mainLabel={page.title}
      {...(page.coverUrl ? { coverImageUrl: page.coverUrl } : {})}
      beforeMain={titleHeader}
    >
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
        <ShareMonthGrid events={events} timezone={pageTimezone} />
      )}
    </PublicPageLayout>
  );
}
