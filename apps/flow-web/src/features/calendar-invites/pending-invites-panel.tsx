/**
 * PendingInvitesPanel — side widget for the /calendar page that lists
 * pending event invites addressed to the authenticated caller.
 *
 * Read-only: the canonical RSVP flow is via the magic-link email, whose
 * plaintext token is required by the public accept endpoint. The listing
 * API only exposes a public id, so this panel surfaces the invites as a
 * summary inbox with a hint to check email for the RSVP link.
 */

import type { components } from '@nodate-flow/sdk';
import Card from '@nodate-flow/ui/primitives/card';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { useQuery } from '@tanstack/react-query';
import { Calendar as CalendarIcon, Clock, MapPin } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { apiRequest } from '../../lib/api';
import { formatApiError } from '../../lib/api-error';
import { formatEpochDateTime } from '../../lib/format';
import styles from './pending-invites-panel.module.css';

type MyInvite = components['schemas']['MyInviteResponse'];

/**
 * Non-suspense query hook for listing the caller's pending invites.
 * Uses a non-suspense query so the panel renders its own loading state
 * alongside the calendar grid rather than blocking the whole route.
 */
function useMyInvitesQuery() {
  return useQuery({
    queryKey: ['me', 'invites'] as const,
    staleTime: 30_000,
    queryFn: async (): Promise<MyInvite[]> => {
      const data = await apiRequest(
        (client) => client.GET('/me/invites'),
        'Failed to load invites',
      );
      return data.invites ?? [];
    },
  });
}

/**
 * Format the event when-range from unix-seconds start/end and all-day flag.
 *
 * Matches the legacy time-web behaviour: all-day collapses to a date,
 * same-day ranges collapse to "start – endTime", cross-day ranges keep
 * both datetimes.
 */
function formatWhen(invite: MyInvite, locale: string): string | null {
  if (!invite.eventStartAt) return null;
  const startMs = invite.eventStartAt * 1000;
  const endMs = invite.eventEndAt ? invite.eventEndAt * 1000 : null;

  if (invite.eventAllDay) {
    try {
      return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(startMs));
    } catch {
      return null;
    }
  }

  const start = formatEpochDateTime(invite.eventStartAt, locale);
  if (!start) return null;

  if (endMs === null || !invite.eventEndAt) return start;

  // Same calendar day → start datetime + end time only.
  const startDate = new Date(startMs);
  const endDate = new Date(endMs);
  const sameDay =
    startDate.getFullYear() === endDate.getFullYear() &&
    startDate.getMonth() === endDate.getMonth() &&
    startDate.getDate() === endDate.getDate();

  if (sameDay) {
    try {
      const endTime = new Intl.DateTimeFormat(locale, { timeStyle: 'short' }).format(endDate);
      return `${start} – ${endTime}`;
    } catch {
      return start;
    }
  }

  const end = formatEpochDateTime(invite.eventEndAt, locale);
  return end ? `${start} – ${end}` : start;
}

interface InviteCardProps {
  invite: MyInvite;
}

function InviteRow({ invite }: InviteCardProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';

  const whenLabel = formatWhen(invite, locale) ?? t('invites.inbox.undated');

  const nowSec = Math.floor(Date.now() / 1000);
  const secondsLeft = invite.expiresAt - nowSec;
  let expiresLabel: string;
  if (secondsLeft <= 0) {
    expiresLabel = t('invites.inbox.expired');
  } else if (secondsLeft < 24 * 60 * 60) {
    expiresLabel = t('invites.inbox.expires_in_hours', {
      count: Math.max(1, Math.round(secondsLeft / 3600)),
    });
  } else {
    const formatted = formatEpochDateTime(invite.expiresAt, locale) ?? '';
    expiresLabel = t('invites.inbox.expires_at', { date: formatted });
  }

  return (
    <li className={styles.inviteListItem}>
      <Card>
        <p className={styles.eventTitle}>{invite.eventTitle}</p>

        <p className={styles.metaLine}>
          <Clock size={12} aria-hidden="true" />
          <span>{whenLabel}</span>
        </p>

        {invite.eventLocation ? (
          <p className={styles.metaLine}>
            <MapPin size={12} aria-hidden="true" />
            <span>{invite.eventLocation}</span>
          </p>
        ) : null}

        <p className={styles.calendarLine}>
          <CalendarIcon size={12} aria-hidden="true" />
          <span>
            {t('invites.inbox.calendar_workspace', {
              calendar: invite.calendarName,
              workspace: invite.workspaceName,
            })}
          </span>
        </p>

        <p className={styles.expiresLine}>{expiresLabel}</p>
      </Card>
    </li>
  );
}

function PanelHeader({ count }: { count: number | null }): ReactElement {
  const { t } = useTranslation('common');
  return (
    <header className={styles.header}>
      <h2 className={styles.headerTitle}>{t('invites.inbox.title')}</h2>
      {count !== null && count > 0 ? (
        <span
          role="img"
          aria-label={t('invites.inbox.count_badge', { count })}
          className={styles.countBadge}
        >
          {count}
        </span>
      ) : null}
    </header>
  );
}

/**
 * PendingInvitesPanel renders the caller's pending event invites as a
 * compact vertical list. Intended to sit beside the month grid on the
 * /calendar route.
 */
export default function PendingInvitesPanel(): ReactElement {
  const { t } = useTranslation('common');
  const { data, isLoading, error } = useMyInvitesQuery();

  if (isLoading) {
    return (
      <aside aria-label={t('invites.inbox.title')} className={styles.panel}>
        <PanelHeader count={null} />
        <Skeleton className={styles.loadingSkeleton} />
        <Skeleton className={styles.loadingSkeleton} />
      </aside>
    );
  }

  if (error) {
    const message = formatApiError(error, t, 'invites.inbox.load_error');
    return (
      <aside aria-label={t('invites.inbox.title')} className={styles.panel}>
        <PanelHeader count={null} />
        <Card>
          <p className={styles.errorMessage} role="alert">
            {message}
          </p>
        </Card>
      </aside>
    );
  }

  const invites = data ?? [];

  return (
    <aside aria-label={t('invites.inbox.title')} className={styles.panel}>
      <PanelHeader count={invites.length} />

      {invites.length === 0 ? (
        <Card>
          <p className={styles.emptyMessage}>{t('invites.inbox.empty')}</p>
        </Card>
      ) : (
        <>
          <p className={styles.rsvpHint}>{t('invites.inbox.rsvp_hint')}</p>
          <ul className={styles.inviteList}>
            {invites.map((invite) => (
              <InviteRow key={invite.id} invite={invite} />
            ))}
          </ul>
        </>
      )}
    </aside>
  );
}
