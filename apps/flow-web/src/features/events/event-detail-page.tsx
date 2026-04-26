/**
 * EventDetailPage — deep-linkable detail surface for a single calendar
 * event at `/workspaces/{wsId}/calendars/{calId}/events/{evtId}`.
 *
 * Layout:
 *   - Back link to `/calendar`.
 *   - Header card: kind badge, title, formatted start/end, all-day flag,
 *     calendar color dot. Owner is intentionally not surfaced because
 *     `EventResponse` does not expose `ownerUserId`.
 *   - Tabs ({@link Tabs}) with two panes — Attendees, Invites — each
 *     wrapped in its own {@link Suspense} boundary so a failure or load
 *     in one pane does not blank the other.
 *
 * Hooks consumed:
 *   - {@link useEventQuery}              — suspense single-event read
 *   - {@link useAttendeesQuery}          — via {@link AttendeesTab}
 *   - {@link useEventInvitesQuery}       — via {@link InvitesTab}
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import Tabs, { type TabItem } from '@nodate-flow/ui/primitives/tabs';
import { Link, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import { useEventQuery } from './api';
import AttendeesTab from './attendees-tab';
import styles from './event-detail-page.module.css';
import InvitesTab from './invites-tab';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/calendars/$calId/events/$evtId');

/** Subset of event kinds we surface a localised badge label for. */
type EventKindLabel = 'event' | 'holiday' | 'task' | 'busy';

/** Resolve the i18n key for an event kind badge label. */
function kindLabelKey(
  kind: string,
):
  | 'event.detail.kind.event'
  | 'event.detail.kind.holiday'
  | 'event.detail.kind.task'
  | 'event.detail.kind.busy' {
  const k = kind as EventKindLabel;
  switch (k) {
    case 'holiday':
      return 'event.detail.kind.holiday';
    case 'task':
      return 'event.detail.kind.task';
    case 'busy':
      return 'event.detail.kind.busy';
    default:
      return 'event.detail.kind.event';
  }
}

/** Format a unix-second timestamp for the header time row. */
function formatEpoch(epochSec: number, locale: string, allDay: boolean): string {
  try {
    return new Intl.DateTimeFormat(locale, {
      dateStyle: 'medium',
      ...(allDay ? {} : { timeStyle: 'short' }),
    }).format(new Date(epochSec * 1000));
  } catch {
    return '';
  }
}

/** Header card — kind, title, start/end, all-day flag, calendar color dot. */
interface EventHeaderProps {
  workspaceId: string;
  calendarId: string;
  eventId: string;
}

function EventHeader({ workspaceId, calendarId, eventId }: EventHeaderProps): ReactElement {
  const { t, i18n } = useTranslation('calendar-events');
  const locale = i18n.resolvedLanguage ?? 'en';
  const { data: event } = useEventQuery(workspaceId, calendarId, eventId);

  const allDay = event.allDay ?? false;
  const startDisplay =
    event.startAt !== undefined ? formatEpoch(event.startAt, locale, allDay) : '';
  const endDisplay = event.endAt !== undefined ? formatEpoch(event.endAt, locale, allDay) : '';

  return (
    <header className={styles.headerCard}>
      <div className={styles.titleRow}>
        <Badge tone="accent">{t(kindLabelKey(event.kind))}</Badge>
        <h1 className={styles.title}>{event.title}</h1>
      </div>
      <dl className={styles.metaGrid}>
        {startDisplay ? (
          <div className={styles.metaItem}>
            <dt className={styles.metaLabel}>{t('field.start')}</dt>
            <dd className={styles.metaValue}>{startDisplay}</dd>
          </div>
        ) : null}
        {endDisplay ? (
          <div className={styles.metaItem}>
            <dt className={styles.metaLabel}>{t('field.end')}</dt>
            <dd className={styles.metaValue}>{endDisplay}</dd>
          </div>
        ) : null}
        {allDay ? (
          <div className={styles.metaItem}>
            <dt className={styles.metaLabel}>{t('field.allDay')}</dt>
            <dd className={styles.metaValue}>{t('event.detail.all_day')}</dd>
          </div>
        ) : null}
        {event.location ? (
          <div className={styles.metaItem}>
            <dt className={styles.metaLabel}>{t('field.location')}</dt>
            <dd className={styles.metaValue}>{event.location}</dd>
          </div>
        ) : null}
      </dl>
    </header>
  );
}

/** Suspense fallback shared by the page-level boundary and tab panes. */
function PaneFallback(): ReactElement {
  const { t } = useTranslation('common');
  return (
    <div className={styles.fallback}>
      <Spinner label={t('common.loading')} />
    </div>
  );
}

/**
 * EventDetailPage — see file-level docstring.
 *
 * Reads its path params via `getRouteApi(...).useParams()` so the
 * component stays decoupled from the route registration. The
 * `useParams` typing surfaces `id` (the workspace id), `calId`, and
 * `evtId`.
 */
export default function EventDetailPage(): ReactElement {
  const { t } = useTranslation('calendar-events');
  const { id: workspaceId, calId: calendarId, evtId: eventId } = routeApi.useParams();

  const tabs: TabItem[] = [
    {
      value: 'attendees',
      label: t('event.detail.tab.attendees'),
      content: (
        <Suspense fallback={<PaneFallback />}>
          <AttendeesTab workspaceId={workspaceId} calendarId={calendarId} eventId={eventId} />
        </Suspense>
      ),
    },
    {
      value: 'invites',
      label: t('event.detail.tab.invites'),
      content: (
        <Suspense fallback={<PaneFallback />}>
          <InvitesTab workspaceId={workspaceId} calendarId={calendarId} eventId={eventId} />
        </Suspense>
      ),
    },
  ];

  return (
    <section className={styles.page}>
      <Link to="/calendar" className={styles.back}>
        {t('event.detail.back')}
      </Link>
      <Suspense fallback={<PaneFallback />}>
        <EventHeader workspaceId={workspaceId} calendarId={calendarId} eventId={eventId} />
      </Suspense>
      <Tabs items={tabs} defaultValue="attendees" aria-label={t('event.detail.tab.attendees')} />
    </section>
  );
}
