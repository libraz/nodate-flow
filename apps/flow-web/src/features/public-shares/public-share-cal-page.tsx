/**
 * PublicShareCalPage — standalone read-only page that renders a public
 * calendar share by URL token. Accessible at /share/cal/{token} without
 * authentication; no sidebar, topbar, or navigation chrome.
 *
 * Backed by GET /share/cal/{token}. The page surfaces its own
 * invalid/expired error states rather than escalating to the root
 * FatalFallback so anonymous visitors land on a branded retry page.
 */

import type { components } from '@nodate-flow/sdk';
import Card from '@nodate-flow/ui/primitives/card';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { useQuery } from '@tanstack/react-query';
import { Calendar as CalendarIcon, Globe, MapPin } from 'lucide-react';
import { type ReactElement, memo } from 'react';
import { useTranslation } from 'react-i18next';

import PublicPageLayout from '../../components/public-page-layout';
import { ApiError, isNetworkError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

/**
 * Approximate hydrated height of a single ShareEventCard. The skeleton
 * loader places three of these so the page does not jump on first paint
 * when events render with title + when label + optional location/memo.
 */
const SHARE_EVENT_CARD_HEIGHT = '5rem';

type SharePageDTO = components['schemas']['PublicShareRenderPage'];
type ShareEventDTO = components['schemas']['PublicShareRenderEvent'];

interface ShareRenderBody {
  page: SharePageDTO;
  events: ShareEventDTO[];
}

/**
 * useShareRenderQuery — fetches the public share payload for the given
 * URL token. `throwOnError` is explicitly disabled so the route can
 * render its own branded invalid/expired fallback instead of bubbling
 * up to the root ErrorBoundary (which would show the generic fatal
 * screen to anonymous visitors).
 */
function useShareRenderQuery(token: string) {
  return useQuery({
    queryKey: ['share', 'cal', token],
    queryFn: async (): Promise<ShareRenderBody> => {
      const result = await sdk.GET('/share/cal/{token}', {
        params: { path: { token } },
      });
      if (result.error || !result.data) {
        throw toApiError(result.error, 'Failed to load shared calendar');
      }
      return {
        page: result.data.page,
        events: result.data.events ?? [],
      };
    },
    retry: (count, err) => {
      if (err instanceof ApiError && isTerminalShareError(err.code)) {
        return false;
      }
      return count < 2;
    },
    throwOnError: false,
  });
}

function isTerminalShareError(code: string | undefined): boolean {
  return code === 'SHARE.SHARE.EXPIRED' || code === 'SHARE.SHARE.TOKEN_INVALID';
}

export interface PublicShareCalPageProps {
  token: string;
}

export default function PublicShareCalPage({ token }: PublicShareCalPageProps): ReactElement {
  const { t } = useTranslation();
  const { data, isLoading, error } = useShareRenderQuery(token);

  if (isLoading) {
    return (
      <PublicPageLayout busy>
        <Skeleton style={{ height: '3rem' }} />
        <Skeleton style={{ height: SHARE_EVENT_CARD_HEIGHT }} />
        <Skeleton style={{ height: SHARE_EVENT_CARD_HEIGHT }} />
        <Skeleton style={{ height: SHARE_EVENT_CARD_HEIGHT }} />
      </PublicPageLayout>
    );
  }

  if (error || !data) {
    const network = isNetworkError(error);
    const isExpired = error instanceof ApiError && error.code === 'SHARE.SHARE.EXPIRED';
    const titleKey = network
      ? 'common.network_error'
      : isExpired
        ? 'share.error.title_expired'
        : 'share.error.title_invalid';
    return (
      <PublicPageLayout showBrandHeader alignMain="center">
        <CalendarIcon
          size={48}
          style={{ color: 'var(--nf-color-fg-subtle)', alignSelf: 'center' }}
          aria-hidden="true"
        />
        <h1
          style={{
            fontSize: 'var(--nf-text-xl)',
            fontWeight: 'var(--nf-weight-semibold)',
            color: 'var(--nf-color-fg)',
            margin: 0,
            textAlign: 'center',
          }}
        >
          {t(titleKey)}
        </h1>
        <p
          style={{
            color: 'var(--nf-color-fg-muted)',
            margin: 0,
            maxInlineSize: 'var(--nf-measure-narrow)',
            alignSelf: 'center',
            textAlign: 'center',
          }}
        >
          {network ? t('share.error.body_network') : t('share.error.body')}
        </p>
        <a
          href="/"
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            alignSelf: 'center',
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
      </PublicPageLayout>
    );
  }

  const { page, events } = data;
  const pageTimezone = page.timezone || 'UTC';

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
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--nf-space-1)' }}>
            {t('share.holidays_country', { country: page.showHolidaysCountry })}
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
        <section
          aria-label={t('share.brand')}
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}
        >
          {events.map((event) => (
            <ShareEventCard key={event.id} event={event} fallbackTimezone={pageTimezone} />
          ))}
        </section>
      )}
    </PublicPageLayout>
  );
}

interface ShareEventCardProps {
  event: ShareEventDTO;
  fallbackTimezone: string;
}

/**
 * ShareEventCard — single event row with timezone-aware date/time label.
 *
 * All date formatting goes through `Intl.DateTimeFormat` with an explicit
 * `timeZone` so the label matches the page's publishing zone regardless
 * of the visitor's local timezone. Times are localized via `i18n.language`.
 *
 * Memoized so re-renders of the parent (e.g. when an unrelated card
 * changes) don't re-format every event in the list.
 */
const ShareEventCard = memo(ShareEventCardImpl);

function ShareEventCardImpl({ event, fallbackTimezone }: ShareEventCardProps): ReactElement {
  const { t, i18n } = useTranslation();
  const zone = event.timezone || fallbackTimezone;
  const locale = i18n.language || 'en';

  const start = typeof event.startAt === 'number' ? new Date(event.startAt * 1000) : null;
  const end = typeof event.endAt === 'number' ? new Date(event.endAt * 1000) : null;

  const whenLabel = formatEventWhen({ start, end, allDay: event.allDay, zone, locale, t });

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

interface FormatWhenArgs {
  start: Date | null;
  end: Date | null;
  allDay: boolean;
  zone: string;
  locale: string;
  t: (key: string) => string;
}

/**
 * formatEventWhen — builds the human-readable "when" label for an event
 * card. Uses `Intl.DateTimeFormat` with the page timezone so the value
 * matches the publisher's view, never the visitor's local zone.
 *
 * Behaviour mirrors the original luxon-based formatter from time-web:
 *   - no start: "date to be announced"
 *   - all-day: date only (medium)
 *   - same-day start/end: full start + short-time end
 *   - cross-day start/end: full start + full end
 *   - start only: full start
 */
function formatEventWhen({ start, end, allDay, zone, locale, t }: FormatWhenArgs): string {
  if (!start) return t('share.event_undated');

  const dateMedium = new Intl.DateTimeFormat(locale, {
    dateStyle: 'medium',
    timeZone: zone,
  });
  const dateTimeMedium = new Intl.DateTimeFormat(locale, {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: zone,
  });
  const timeShort = new Intl.DateTimeFormat(locale, {
    timeStyle: 'short',
    timeZone: zone,
  });

  if (allDay) return safeFormat(() => dateMedium.format(start), start.toISOString());
  if (end && isSameZonedDay(start, end, zone)) {
    return `${safeFormat(() => dateTimeMedium.format(start), start.toISOString())} – ${safeFormat(
      () => timeShort.format(end),
      end.toISOString(),
    )}`;
  }
  if (end) {
    return `${safeFormat(() => dateTimeMedium.format(start), start.toISOString())} – ${safeFormat(
      () => dateTimeMedium.format(end),
      end.toISOString(),
    )}`;
  }
  return safeFormat(() => dateTimeMedium.format(start), start.toISOString());
}

/**
 * isSameZonedDay — true when both timestamps fall on the same calendar
 * day when viewed in the given IANA timezone. Uses the `en-CA` short
 * date format (`YYYY-MM-DD`) as a stable comparison key that is
 * unaffected by the user's display locale.
 */
function isSameZonedDay(a: Date, b: Date, zone: string): boolean {
  try {
    const fmt = new Intl.DateTimeFormat('en-CA', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      timeZone: zone,
    });
    return fmt.format(a) === fmt.format(b);
  } catch {
    return a.toDateString() === b.toDateString();
  }
}

function safeFormat(fn: () => string, fallback: string): string {
  try {
    return fn();
  } catch {
    return fallback;
  }
}
