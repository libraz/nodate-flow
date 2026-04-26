/**
 * InvitesTab — Invites pane of the calendar event detail page.
 *
 * Read + revoke surface only. Issuance of a new magic-link invite is
 * anchored on a specific attendee row and lives in the Attendees tab
 * via {@link useCreateAttendeeInviteMutation}; this pane lists invites
 * already issued for the event and exposes revoke per row.
 *
 * The list endpoint returns {@link InviteSummaryResponse}, which omits
 * the magic-link token by design (only the issuance response surfaces
 * it once for copy-to-clipboard). That means there is no way to
 * reconstruct the accept URL from a row here, so this pane intentionally
 * does not expose a "Copy link" action — issuing a fresh invite from
 * the Attendees tab is the sanctioned way to obtain a usable token.
 *
 * Hooks:
 *   - {@link useEventInvitesQuery}             — suspense list read
 *   - {@link useRevokeEventInviteMutation}     — DELETE per invite
 *
 * Wrap in a `<Suspense>` boundary at the call site; this component
 * itself does not fall back.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import styles from './event-detail-page.module.css';
import {
  type EventInvite,
  useEventInvitesQuery,
  useRevokeEventInviteMutation,
} from './invites-api';

export interface InvitesTabProps {
  workspaceId: string;
  calendarId: string;
  eventId: string;
}

/**
 * Format a unix-second `expiresAt` value as a localised date+time, or
 * fall back to an empty string when the input is missing/invalid.
 */
function formatExpiry(epochSec: number, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(
      new Date(epochSec * 1000),
    );
  } catch {
    return '';
  }
}

/**
 * Resolve the badge tone for an invite based on its lifecycle:
 *   - accepted  → success
 *   - expired   → danger
 *   - sent      → info
 *   - otherwise → neutral (newly created, awaiting send)
 */
function inviteTone(
  invite: EventInvite,
  nowSec: number,
): 'success' | 'danger' | 'info' | 'neutral' {
  if (invite.acceptedAt && invite.acceptedAt > 0) return 'success';
  if (invite.expiresAt > 0 && invite.expiresAt < nowSec) return 'danger';
  if (invite.sentAt && invite.sentAt > 0) return 'info';
  return 'neutral';
}

/** i18n key for an invite's lifecycle state. */
function inviteStatusKey(
  invite: EventInvite,
  nowSec: number,
):
  | 'event.invites.status.accepted'
  | 'event.invites.status.expired'
  | 'event.invites.status.sent'
  | 'event.invites.status.pending' {
  if (invite.acceptedAt && invite.acceptedAt > 0) return 'event.invites.status.accepted';
  if (invite.expiresAt > 0 && invite.expiresAt < nowSec) return 'event.invites.status.expired';
  if (invite.sentAt && invite.sentAt > 0) return 'event.invites.status.sent';
  return 'event.invites.status.pending';
}

/**
 * InvitesTab — see file-level docstring.
 */
export default function InvitesTab({
  workspaceId,
  calendarId,
  eventId,
}: InvitesTabProps): ReactElement {
  const { t, i18n } = useTranslation('calendar-events');
  const locale = i18n.resolvedLanguage ?? 'en';

  const { data: invites } = useEventInvitesQuery(workspaceId, calendarId, eventId);
  const revoke = useRevokeEventInviteMutation();

  const nowSec = Math.floor(Date.now() / 1000);

  const handleRevoke = async (inviteId: string): Promise<void> => {
    const confirmed = await confirmAction({
      title: t('event.invites.revoke_confirm_title'),
      message: t('event.invites.revoke_confirm'),
      tone: 'danger',
    });
    if (!confirmed) return;
    revoke.mutate(
      { wsId: workspaceId, calId: calendarId, evtId: eventId, inviteId },
      {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('event.invites.revoke_success') });
        },
        onError: () => {
          toaster.show({ tone: 'danger', message: t('event.invites.revoke_error') });
        },
      },
    );
  };

  if (invites.length === 0) {
    return (
      <div className={styles.tabPanel}>
        <p className={styles.invitesEmpty}>{t('event.invites.empty')}</p>
        <p className={styles.invitesHint}>{t('event.invites.create_hint')}</p>
      </div>
    );
  }

  return (
    <div className={styles.tabPanel}>
      <ul className={styles.invitesList}>
        {invites.map((invite) => {
          const tone = inviteTone(invite, nowSec);
          const statusKey = inviteStatusKey(invite, nowSec);
          const expiryDisplay = formatExpiry(invite.expiresAt, locale);
          return (
            <li key={invite.id} className={styles.inviteRow}>
              <div className={styles.inviteIdentity}>
                <span className={styles.inviteEmail}>{invite.email}</span>
                {expiryDisplay ? (
                  <span className={styles.inviteExpiry}>
                    {t('event.invites.expires_at', { date: expiryDisplay })}
                  </span>
                ) : null}
              </div>
              <Badge tone={tone}>{t(statusKey)}</Badge>
              <div className={styles.inviteControls}>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    void handleRevoke(invite.id);
                  }}
                  disabled={revoke.isPending}
                >
                  {t('event.invites.revoke')}
                </Button>
              </div>
            </li>
          );
        })}
      </ul>
      <p className={styles.invitesHint}>{t('event.invites.create_hint')}</p>
    </div>
  );
}
