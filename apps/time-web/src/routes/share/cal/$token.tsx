import { useQuery } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { Calendar as CalendarIcon, Globe, MapPin } from 'lucide-react';
import { DateTime } from 'luxon';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import Card from '@nodate-flow/ui/primitives/card';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';

import { ApiError, toApiError } from '../../../lib/api-error';
import { flowWebUrl, sdk } from '../../../lib/sdk';

interface SharePageDTO {
  title: string;
  description?: string;
  iconUrl?: string;
  coverUrl?: string;
  timezone: string;
  showHolidaysCountry?: string;
  workspaceId: string;
  workspaceName: string;
  createdAt: number;
}

interface ShareEventDTO {
  id: string;
  title: string;
  startAt?: number;
  endAt?: number;
  allDay: boolean;
  timezone: string;
  location?: string;
  memo?: string;
  url?: string;
  kind: string;
  showAs: string;
  blockLabel?: string;
  recurrenceRule?: string;
  recurrenceEnd?: number;
}

interface ShareRenderBody {
  page: SharePageDTO;
  events: ShareEventDTO[];
}

function useShareRenderQuery(token: string) {
  return useQuery({
    queryKey: ['share', 'cal', token],
    queryFn: async () => {
      const result = await sdk.GET('/share/cal/{token}', {
        params: { path: { token } },
      });
      if (result.error || !result.data) {
        throw toApiError(result.error, 'Failed to load shared calendar');
      }
      return result.data as ShareRenderBody;
    },
    retry: (count, err) => {
      if (err instanceof ApiError && isTerminalShareError(err.code)) {
        return false;
      }
      return count < 2;
    },
    // The shared SDK queryClient defaults to `throwOnError: true`, which would
    // bypass the branded invalid/expired fallback below and surface the root
    // FatalFallback to anonymous visitors. Opt out locally so the route owns
    // its own error rendering. TODO(simplify): revisit once the global default
    // is flipped to opt-in (see bug 2026-04-23-web-share-route-throw-on-error-bypass).
    throwOnError: false,
  });
}

function isTerminalShareError(code: string | undefined): boolean {
  return code === 'SHARE.SHARE.EXPIRED' || code === 'SHARE.SHARE.TOKEN_INVALID';
}

export const Route = createFileRoute('/share/cal/$token')({
  component: SharePage,
});

function SharePage(): ReactElement {
  const { token } = Route.useParams();
  const { t } = useTranslation();
  const { data, isLoading, error } = useShareRenderQuery(token);

  if (isLoading) {
    return (
      <main
        style={{
          maxWidth: '48rem',
          marginInline: 'auto',
          padding: 'var(--nf-space-6) var(--nf-space-4)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-3)',
        }}
      >
        <Skeleton style={{ height: '3rem' }} />
        <Skeleton style={{ height: '4rem' }} />
        <Skeleton style={{ height: '4rem' }} />
        <Skeleton style={{ height: '4rem' }} />
      </main>
    );
  }

  if (error || !data) {
    const isExpired = error instanceof ApiError && error.code === 'SHARE.SHARE.EXPIRED';
    const titleKey = isExpired ? 'share.error.title_expired' : 'share.error.title_invalid';
    return (
      <div
        style={{
          minHeight: '100vh',
          display: 'flex',
          flexDirection: 'column',
          backgroundColor: 'var(--nf-color-bg)',
        }}
      >
        <header
          style={{
            maxWidth: '48rem',
            width: '100%',
            marginInline: 'auto',
            padding: 'var(--nf-space-4)',
            display: 'flex',
            alignItems: 'center',
          }}
        >
          <a
            href={flowWebUrl}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 'var(--nf-space-2)',
              color: 'var(--nf-color-fg)',
              fontWeight: 'var(--nf-weight-semibold)',
              fontSize: 'var(--nf-text-base)',
              textDecoration: 'none',
            }}
          >
            <CalendarIcon
              size={20}
              style={{ color: 'var(--nf-color-accent)' }}
              aria-hidden="true"
            />
            {t('share.brand')}
          </a>
        </header>
        <main
          style={{
            flex: 1,
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            justifyContent: 'center',
            gap: 'var(--nf-space-4)',
            padding: 'var(--nf-space-6) var(--nf-space-4)',
            textAlign: 'center',
          }}
        >
          <CalendarIcon
            size={48}
            style={{ color: 'var(--nf-color-fg-subtle)' }}
            aria-hidden="true"
          />
          <h1
            style={{
              fontSize: 'var(--nf-text-xl)',
              fontWeight: 'var(--nf-weight-semibold)',
              color: 'var(--nf-color-fg)',
              margin: 0,
            }}
          >
            {t(titleKey)}
          </h1>
          <p
            style={{
              color: 'var(--nf-color-fg-muted)',
              margin: 0,
              maxWidth: '32rem',
            }}
          >
            {t('share.error.body')}
          </p>
          <a
            href={flowWebUrl}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              marginBlockStart: 'var(--nf-space-2)',
              paddingInline: 'var(--nf-space-4)',
              paddingBlock: 'var(--nf-space-2)',
              borderRadius: 'var(--nf-radius-md)',
              backgroundColor: 'var(--nf-color-accent)',
              color: 'var(--nf-color-accent-fg)',
              textDecoration: 'none',
              fontWeight: 'var(--nf-weight-medium)',
              fontSize: 'var(--nf-text-sm)',
            }}
          >
            {t('share.error.back')}
          </a>
        </main>
      </div>
    );
  }

  const { page, events } = data;
  const pageTimezone = page.timezone || 'UTC';

  return (
    <div style={{ minHeight: '100vh', backgroundColor: 'var(--nf-color-bg)' }}>
      {page.coverUrl ? (
        <div
          style={{
            height: '12rem',
            backgroundImage: `url(${encodeURI(page.coverUrl)})`,
            backgroundSize: 'cover',
            backgroundPosition: 'center',
            backgroundColor: 'var(--nf-color-bg-subtle)',
          }}
          aria-hidden="true"
        />
      ) : null}

      <header
        style={{
          maxWidth: '48rem',
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
                width: '2.5rem',
                height: '2.5rem',
                borderRadius: 'var(--nf-radius-md)',
                objectFit: 'cover',
              }}
            />
          ) : (
            <div
              style={{
                width: '2.5rem',
                height: '2.5rem',
                borderRadius: 'var(--nf-radius-md)',
                backgroundColor: 'var(--nf-color-accent-subtle)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
              aria-hidden="true"
            >
              <CalendarIcon size={20} style={{ color: 'var(--nf-color-accent)' }} />
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
            <span
              style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--nf-space-1)' }}
            >
              {t('share.holidays_country', { country: page.showHolidaysCountry })}
            </span>
          ) : null}
        </div>
      </header>

      <main
        style={{
          maxWidth: '48rem',
          marginInline: 'auto',
          padding: '0 var(--nf-space-4) var(--nf-space-6)',
        }}
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
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
            {events.map((event) => (
              <ShareEventCard key={event.id} event={event} fallbackTimezone={pageTimezone} />
            ))}
          </div>
        )}
      </main>
    </div>
  );
}

interface ShareEventCardProps {
  event: ShareEventDTO;
  fallbackTimezone: string;
}

function ShareEventCard({ event, fallbackTimezone }: ShareEventCardProps): ReactElement {
  const { t } = useTranslation();
  const zone = event.timezone || fallbackTimezone;
  const start = event.startAt ? DateTime.fromSeconds(event.startAt, { zone }) : null;
  const end = event.endAt ? DateTime.fromSeconds(event.endAt, { zone }) : null;

  let whenLabel: string;
  if (!start) {
    whenLabel = t('share.event_undated');
  } else if (event.allDay) {
    whenLabel = start.toLocaleString(DateTime.DATE_MED);
  } else if (end?.hasSame(start, 'day')) {
    whenLabel = `${start.toLocaleString(DateTime.DATETIME_MED)} – ${end.toLocaleString(DateTime.TIME_SIMPLE)}`;
  } else if (end) {
    whenLabel = `${start.toLocaleString(DateTime.DATETIME_MED)} – ${end.toLocaleString(DateTime.DATETIME_MED)}`;
  } else {
    whenLabel = start.toLocaleString(DateTime.DATETIME_MED);
  }

  return (
    <Card>
      <p style={{ fontWeight: 'var(--nf-weight-medium)', color: 'var(--nf-color-fg)', margin: 0 }}>
        {event.title}
      </p>
      <p
        style={{
          marginBlockStart: 'var(--nf-space-1)',
          fontSize: 'var(--nf-text-sm)',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {whenLabel}
      </p>
      {event.location ? (
        <p
          style={{
            marginBlockStart: 'var(--nf-space-1)',
            fontSize: 'var(--nf-text-sm)',
            color: 'var(--nf-color-fg-muted)',
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--nf-space-1)',
          }}
        >
          <MapPin size={12} aria-hidden="true" />
          {event.location}
        </p>
      ) : null}
      {event.memo ? (
        <p
          style={{
            marginBlockStart: 'var(--nf-space-2)',
            fontSize: 'var(--nf-text-sm)',
            color: 'var(--nf-color-fg-muted)',
            whiteSpace: 'pre-wrap',
          }}
        >
          {event.memo}
        </p>
      ) : null}
    </Card>
  );
}
