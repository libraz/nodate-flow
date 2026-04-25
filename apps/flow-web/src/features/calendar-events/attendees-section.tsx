/**
 * AttendeesSection — manages the attendee roster surfaced inside the
 * unified EventDialog for `event` and `milestone` calendar items.
 *
 * Capabilities are gated by the actor's relationship to the event:
 * - When the actor is the event owner, every attendee row exposes a
 *   can-edit toggle, an "invite by email" action, and a remove control.
 * - When the actor is also an attendee, the "your response" RSVP picker
 *   appears at the top of the section.
 * - When the actor is not the owner but is an attendee, only the RSVP
 *   picker and a self-row remove control are interactive.
 *
 * The component owns no submission state — every action funnels through
 * a react-query mutation in {@link ./attendees-api.ts}, and the section
 * re-renders off the invalidated `useAttendeesQuery` cache.
 */

import Avatar from '@nodate-flow/ui/primitives/avatar';
import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import SegmentedControl, {
  type SegmentedControlOption,
} from '@nodate-flow/ui/primitives/segmented-control';
import Switch from '@nodate-flow/ui/primitives/switch';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspaceMembersQuery } from '../workspaces/api';
import {
  type Attendee,
  type Rsvp,
  useAddAttendeesMutation,
  useAttendeesQuery,
  useCreateAttendeeInviteMutation,
  useRemoveAttendeeMutation,
  useToggleCanEditMutation,
  useUpdateOwnRsvpMutation,
} from './attendees-api';
import styles from './attendees-section.module.css';

export interface AttendeesSectionProps {
  workspaceId: string;
  calendarId: string;
  eventId: string;
  /**
   * Owner user id of the event — controls who can manage can-edit /
   * remove / invite. Pass null when the upstream event response does
   * not surface ownerUserId; controls degrade to disabled.
   */
  ownerUserId: string | null;
  /** Current actor's user id, used to detect "self" rows for RSVP control. */
  selfUserId: string;
}

/** Closed set of RSVP options shown in the actor's response picker. */
const RSVP_OPTIONS: readonly Rsvp[] = ['accepted', 'tentative', 'declined', 'pending'];

/** Map an RSVP value to a Badge tone. Treat unknown server values as neutral. */
function rsvpTone(rsvp: string): BadgeTone {
  switch (rsvp) {
    case 'accepted':
      return 'success';
    case 'declined':
      return 'danger';
    case 'tentative':
      return 'warning';
    default:
      return 'neutral';
  }
}

/** Two-letter initials fallback for avatars without a picture. */
function initialsOf(displayName: string): string {
  const trimmed = displayName.trim();
  if (!trimmed) return '?';
  const parts = trimmed.split(/\s+/u);
  const first = parts[0]?.[0] ?? '';
  const second = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : '';
  return (first + second).toUpperCase();
}

/**
 * AttendeesSection — see file-level docstring.
 */
export default function AttendeesSection({
  workspaceId,
  calendarId,
  eventId,
  ownerUserId,
  selfUserId,
}: AttendeesSectionProps): ReactElement {
  const { t } = useTranslation('calendar-events');

  const scope = { workspaceId, calendarId, eventId };
  const attendeesQuery = useAttendeesQuery(scope);
  const membersQuery = useWorkspaceMembersQuery(workspaceId);

  const addAttendees = useAddAttendeesMutation();
  const removeAttendee = useRemoveAttendeeMutation();
  const updateOwnRsvp = useUpdateOwnRsvpMutation();
  const toggleCanEdit = useToggleCanEditMutation();
  const createInvite = useCreateAttendeeInviteMutation();

  // Combobox is controlled by an empty string after each successful
  // add — picking a member triggers the mutation and clears the field.
  const [pickerValue, setPickerValue] = useState('');

  const attendees: Attendee[] = attendeesQuery.data ?? [];
  const isOwner = ownerUserId !== null && ownerUserId === selfUserId;
  const selfAttendee = attendees.find((a) => a.userId === selfUserId);

  // Members already on the event (or the actor when already attending)
  // are filtered out of the picker so single-element add is unambiguous.
  const memberOptions = useMemo(() => {
    const attendingIds = new Set(attendees.map((a) => a.userId));
    return (membersQuery.data ?? [])
      .filter((m) => !attendingIds.has(m.userId))
      .map((m) => ({ value: m.userId, label: m.displayName }));
  }, [attendees, membersQuery.data]);

  const handlePick = (userId: string): void => {
    if (!userId) return;
    setPickerValue('');
    addAttendees.mutate(
      { ...scope, userIds: [userId] },
      {
        onError: () => {
          toaster.show({ tone: 'danger', message: t('event.attendees.add') });
        },
      },
    );
  };

  const rsvpOptions: SegmentedControlOption<Rsvp>[] = RSVP_OPTIONS.map((value) => ({
    value,
    label: t(rsvpLabelKey(value)),
  }));

  const handleRsvpChange = (next: Rsvp): void => {
    updateOwnRsvp.mutate({ ...scope, rsvp: next });
  };

  const handleToggleCanEdit = (userId: string, next: boolean): void => {
    toggleCanEdit.mutate({ ...scope, userId, canEdit: next });
  };

  const handleRemove = (userId: string): void => {
    removeAttendee.mutate({ ...scope, userId });
  };

  const handleInvite = async (attendeeId: string): Promise<void> => {
    try {
      const result = await createInvite.mutateAsync({ ...scope, attendeeId });
      const origin = typeof window !== 'undefined' ? window.location.origin : '';
      const link = `${origin}/invites/accept?token=${result.token}`;
      try {
        await navigator.clipboard.writeText(link);
      } catch {
        // Clipboard write may fail under permissions-deny / non-secure
        // contexts; the toast still confirms the invite was issued.
      }
      toaster.show({ tone: 'success', message: t('event.attendees.invite_sent') });
    } catch {
      toaster.show({ tone: 'danger', message: t('event.attendees.send_invite') });
    }
  };

  return (
    <section className={styles.section} aria-label={t('event.attendees.title')}>
      <div className={styles.header}>
        <span>{t('event.attendees.title')}</span>
        <Badge tone="neutral">{attendees.length}</Badge>
      </div>

      {selfAttendee ? (
        <div className={styles.responseRow}>
          <span className={styles.responseLabel}>{t('event.attendees.your_response')}</span>
          <SegmentedControl
            ariaLabel={t('event.attendees.your_response')}
            fullWidth
            options={rsvpOptions}
            value={(selfAttendee.rsvp as Rsvp) ?? 'pending'}
            onChange={handleRsvpChange}
            disabled={updateOwnRsvp.isPending}
          />
        </div>
      ) : null}

      <div className={styles.addRow}>
        <Combobox
          options={memberOptions}
          value={pickerValue}
          onChange={handlePick}
          placeholder={t('event.attendees.placeholder')}
          aria-label={t('event.attendees.add')}
          disabled={addAttendees.isPending || memberOptions.length === 0}
        />
      </div>

      {attendees.length === 0 ? (
        <p className={styles.empty}>{t('event.attendees.empty')}</p>
      ) : (
        <ul className={styles.list}>
          {attendees.map((a) => {
            const isSelf = a.userId === selfUserId;
            const canRemove = isOwner || isSelf;
            return (
              <li key={a.id} className={styles.row}>
                <Avatar
                  size="sm"
                  alt={a.displayName}
                  initials={initialsOf(a.displayName)}
                  {...(a.avatarUrl ? { src: a.avatarUrl } : {})}
                />
                <div className={styles.identity}>
                  <span className={styles.name}>
                    <span>{a.displayName}</span>
                    {isSelf ? (
                      <span className={styles.youPill}>{t('event.attendees.you')}</span>
                    ) : null}
                  </span>
                </div>
                <Badge tone={rsvpTone(a.rsvp)}>{t(rsvpLabelKey(a.rsvp))}</Badge>
                <div className={styles.controls}>
                  {isOwner && !isSelf ? (
                    <span className={styles.canEdit}>
                      <Switch
                        checked={a.canEdit}
                        onCheckedChange={(next) => handleToggleCanEdit(a.userId, next)}
                        disabled={toggleCanEdit.isPending}
                        aria-label={t('event.attendees.can_edit')}
                      />
                      <span aria-hidden>{t('event.attendees.can_edit')}</span>
                    </span>
                  ) : null}
                  {isOwner ? (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        void handleInvite(a.id);
                      }}
                      disabled={createInvite.isPending}
                    >
                      {t('event.attendees.send_invite')}
                    </Button>
                  ) : null}
                  {canRemove ? (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className={styles.removeButton}
                      onClick={() => handleRemove(a.userId)}
                      disabled={removeAttendee.isPending}
                      aria-label={t('event.attendees.remove')}
                    >
                      {'×'}
                    </Button>
                  ) : null}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

/**
 * Resolve the i18n key for a given RSVP value. Unknown values fall back
 * to the `pending` label so the row never renders an empty string.
 */
function rsvpLabelKey(
  rsvp: string,
):
  | 'event.attendees.rsvp.accepted'
  | 'event.attendees.rsvp.declined'
  | 'event.attendees.rsvp.tentative'
  | 'event.attendees.rsvp.pending' {
  switch (rsvp) {
    case 'accepted':
      return 'event.attendees.rsvp.accepted';
    case 'declined':
      return 'event.attendees.rsvp.declined';
    case 'tentative':
      return 'event.attendees.rsvp.tentative';
    default:
      return 'event.attendees.rsvp.pending';
  }
}
