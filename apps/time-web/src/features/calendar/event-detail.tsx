import {
  CheckSquare,
  Clock,
  ExternalLink,
  MapPin,
  MessageSquare,
  Paperclip,
  Pencil,
  Square,
  Trash2,
  User,
  Users,
  X,
} from 'lucide-react';
import { DateTime } from 'luxon';
import { type ReactElement, useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useCalendarUiStore } from '../../stores/calendar-ui-store';
import { useCalendarEventsQuery, useDeleteEventMutation } from './api';
import { getEventStyle } from './event-styles';
import type { CalendarEvent, EventKind, Rsvp, ShowAs } from './types';

interface ChecklistItem {
  id: string;
  text: string;
  checked: boolean;
}

interface Comment {
  id: string;
  author: string;
  text: string;
  createdAt: string;
}

interface Attachment {
  id: string;
  filename: string;
  size: number;
}

interface Attendee {
  userId: string;
  displayName: string;
  rsvp: Rsvp;
  memberColor: string;
}

function formatDateRange(startAt: string, endAt: string, allDay: boolean, locale: string): string {
  const start = DateTime.fromISO(startAt).setLocale(locale);
  const end = DateTime.fromISO(endAt).setLocale(locale);

  if (allDay) {
    if (start.hasSame(end, 'day') || end.diff(start, 'days').days <= 1) {
      return start.toLocaleString(DateTime.DATE_HUGE);
    }
    return `${start.toLocaleString({ month: 'short', day: 'numeric' })} - ${end.toLocaleString(DateTime.DATE_MED)}`;
  }

  if (start.hasSame(end, 'day')) {
    return `${start.toLocaleString(DateTime.DATE_HUGE)}\n${start.toLocaleString(DateTime.TIME_SIMPLE)} - ${end.toLocaleString(DateTime.TIME_SIMPLE)}`;
  }
  return `${start.toLocaleString(DateTime.DATETIME_MED)} - ${end.toLocaleString(DateTime.DATETIME_MED)}`;
}

function isUrl(text: string): boolean {
  try {
    new URL(text);
    return true;
  } catch {
    return false;
  }
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function useEventById(eventId: string): CalendarEvent | null {
  const { selectedDate, currentView } = useCalendarUiStore();

  const rangeStart = useMemo(() => {
    if (currentView === 'week') {
      const dow = selectedDate.weekday % 7;
      return selectedDate.minus({ days: dow }).toISODate() ?? '';
    }
    const first = DateTime.local(selectedDate.year, selectedDate.month, 1);
    const startDow = first.weekday % 7;
    return first.minus({ days: startDow }).toISODate() ?? '';
  }, [selectedDate, currentView]);

  const rangeEnd = useMemo(() => {
    if (currentView === 'week') {
      const dow = selectedDate.weekday % 7;
      return selectedDate.minus({ days: dow }).plus({ days: 8 }).toISODate() ?? '';
    }
    const first = DateTime.local(selectedDate.year, selectedDate.month, 1);
    const startDow = first.weekday % 7;
    return first.minus({ days: startDow }).plus({ days: 43 }).toISODate() ?? '';
  }, [selectedDate, currentView]);

  const { data: events } = useCalendarEventsQuery(rangeStart, rangeEnd);
  return events?.find((e) => e.id === eventId) ?? null;
}

const badgeBase = 'rounded-full px-2 py-0.5 text-xs font-medium';

export default function EventDetail(): ReactElement | null {
  const { t, i18n } = useTranslation();
  const { eventDetailId, closeEventDetail, openEventModal } = useCalendarUiStore();
  const deleteMutation = useDeleteEventMutation();
  const [confirmDelete, setConfirmDelete] = useState(false);

  // Stubs for features with empty data
  const attendees: Attendee[] = [];
  const checklist: ChecklistItem[] = [];
  const comments: Comment[] = [];
  const attachments: Attachment[] = [];
  const [newComment, setNewComment] = useState('');

  const event = useEventById(eventDetailId ?? '');

  const kindLabels: Record<EventKind, { label: string; className: string }> = {
    event: {
      label: t('event.kindEvent'),
      className: `${badgeBase} bg-[var(--nf-color-accent-subtle)] text-[var(--nf-color-accent)]`,
    },
    block: {
      label: t('event.kindBlock'),
      className: `${badgeBase} bg-[var(--nf-color-bg-sunken)]`,
    },
    free: {
      label: t('event.kindFree'),
      className: `${badgeBase} bg-[var(--nf-color-bg-sunken)]`,
    },
  };

  const showAsLabels: Record<ShowAs, { label: string; className: string }> = {
    busy: {
      label: t('event.showBusy'),
      className: `${badgeBase} bg-[var(--nf-color-accent-subtle)] text-[var(--nf-color-accent)]`,
    },
    free: {
      label: t('event.showFree'),
      className: `${badgeBase} bg-[var(--nf-color-bg-sunken)]`,
    },
    tentative: {
      label: t('event.showTentative'),
      className: `${badgeBase} bg-[var(--nf-color-bg-sunken)]`,
    },
    oof: {
      label: t('event.showOof'),
      className: `${badgeBase} bg-[var(--nf-color-bg-sunken)]`,
    },
  };

  const rsvpLabels: Record<Rsvp, { label: string; className: string }> = {
    pending: {
      label: t('rsvp.pending'),
      className: `${badgeBase} bg-[var(--nf-color-bg-sunken)]`,
    },
    accepted: {
      label: t('rsvp.accepted'),
      className: `${badgeBase} bg-[var(--nf-color-accent-subtle)] text-[var(--nf-color-accent)]`,
    },
    declined: {
      label: t('rsvp.declined'),
      className: `${badgeBase} bg-[var(--nf-color-bg-sunken)]`,
    },
    tentative: {
      label: t('rsvp.tentative'),
      className: `${badgeBase} bg-[var(--nf-color-bg-sunken)]`,
    },
  };

  const handleDelete = useCallback(() => {
    if (!event) return;
    if (!confirmDelete) {
      setConfirmDelete(true);
      return;
    }
    deleteMutation.mutate(
      { calendarId: event.calendarId, eventId: event.id },
      {
        onSuccess: () => {
          closeEventDetail();
          setConfirmDelete(false);
        },
      },
    );
  }, [event, confirmDelete, deleteMutation, closeEventDetail]);

  const handleEdit = useCallback(() => {
    if (!event) return;
    closeEventDetail();
    openEventModal(event.id);
  }, [event, closeEventDetail, openEventModal]);

  if (!eventDetailId) return null;

  if (!event) {
    return (
      <div
        className="fixed inset-0 z-40 flex items-center justify-center sm:relative sm:inset-auto sm:z-auto sm:w-80 sm:shrink-0 sm:border-l sm:border-[var(--nf-color-border)] sm:flex"
        style={{ backgroundColor: 'var(--nf-color-overlay)' }}
      >
        <div
          className="w-full max-w-md rounded-lg p-6 sm:max-w-none sm:rounded-none sm:shadow-none"
          style={{ backgroundColor: 'var(--nf-color-bg-elevated)' }}
        >
          <p className="text-sm" style={{ color: 'var(--nf-color-fg-muted)' }}>
            {t('event.loadingEvent')}
          </p>
        </div>
      </div>
    );
  }

  const kindInfo = kindLabels[event.kind];
  const showAsInfo = showAsLabels[event.showAs];
  const eventColor = '#3b82f6';

  const content = (
    <div className="flex h-full flex-col overflow-y-auto">
      {/* Header bar with color */}
      <div className="flex items-center justify-between border-b border-[var(--nf-color-border)] px-4 py-3">
        <div className="flex items-center gap-2">
          <div
            className="h-3 w-3 shrink-0 rounded-full"
            style={getEventStyle(event.kind, event.showAs, eventColor)}
          />
          <h2 className="truncate text-lg font-semibold">{event.title}</h2>
        </div>
        <button
          type="button"
          onClick={closeEventDetail}
          className="rounded-md p-1 hover:bg-[var(--nf-color-surface-hover)]"
          aria-label={t('common.close')}
        >
          <X className="h-5 w-5" />
        </button>
      </div>

      <div className="flex-1 space-y-4 p-4">
        {/* Badges */}
        <div className="flex flex-wrap gap-2">
          <span className={kindInfo.className}>{kindInfo.label}</span>
          <span className={showAsInfo.className}>{showAsInfo.label}</span>
        </div>

        {/* Date/Time */}
        <div className="flex gap-2 text-sm" style={{ color: 'var(--nf-color-fg)' }}>
          <Clock
            className="mt-0.5 h-4 w-4 shrink-0"
            style={{ color: 'var(--nf-color-fg-subtle)' }}
          />
          <span className="whitespace-pre-line">
            {formatDateRange(event.startAt, event.endAt, event.allDay, i18n.language)}
          </span>
        </div>

        {/* Location */}
        {event.location ? (
          <div className="flex gap-2 text-sm" style={{ color: 'var(--nf-color-fg)' }}>
            <MapPin
              className="mt-0.5 h-4 w-4 shrink-0"
              style={{ color: 'var(--nf-color-fg-subtle)' }}
            />
            {isUrl(event.location) ? (
              <a
                href={event.location}
                target="_blank"
                rel="noopener noreferrer"
                className="flex items-center gap-1 hover:underline"
                style={{ color: 'var(--nf-color-accent)' }}
              >
                {event.location}
                <ExternalLink className="h-3 w-3" />
              </a>
            ) : (
              <span>{event.location}</span>
            )}
          </div>
        ) : null}

        {/* Memo */}
        {event.memo ? (
          <div
            className="rounded-md p-3 text-sm whitespace-pre-wrap"
            style={{
              backgroundColor: 'var(--nf-color-bg-sunken)',
              color: 'var(--nf-color-fg)',
            }}
          >
            {event.memo}
          </div>
        ) : null}

        {/* Owner */}
        <div className="flex items-center gap-2 text-sm">
          <User className="h-4 w-4" style={{ color: 'var(--nf-color-fg-subtle)' }} />
          <div className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: eventColor }} />
          <span style={{ color: 'var(--nf-color-fg)' }}>{t('common.owner')}</span>
        </div>

        {/* Attendees */}
        <div>
          <div className="mb-2 flex items-center gap-2">
            <Users className="h-4 w-4" style={{ color: 'var(--nf-color-fg-subtle)' }} />
            <span className="text-sm font-medium" style={{ color: 'var(--nf-color-fg)' }}>
              {t('detail.attendees')} {attendees.length > 0 ? `(${attendees.length})` : ''}
            </span>
          </div>
          {attendees.length === 0 ? (
            <p className="ml-6 text-xs" style={{ color: 'var(--nf-color-fg-subtle)' }}>
              {t('detail.noAttendees')}
            </p>
          ) : (
            <div className="ml-6 space-y-1.5">
              {attendees.map((a) => {
                const rsvpInfo = rsvpLabels[a.rsvp];
                return (
                  <div key={a.userId} className="flex items-center gap-2">
                    <div
                      className="h-2.5 w-2.5 shrink-0 rounded-full"
                      style={{ backgroundColor: a.memberColor }}
                    />
                    <span className="flex-1 truncate text-sm">{a.displayName}</span>
                    <span className={rsvpInfo.className}>{rsvpInfo.label}</span>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Checklist */}
        <div>
          <div className="mb-2 flex items-center gap-2">
            <CheckSquare className="h-4 w-4" style={{ color: 'var(--nf-color-fg-subtle)' }} />
            <span className="text-sm font-medium" style={{ color: 'var(--nf-color-fg)' }}>
              {t('detail.checklist')}
            </span>
          </div>
          {checklist.length === 0 ? (
            <p className="ml-6 text-xs" style={{ color: 'var(--nf-color-fg-subtle)' }}>
              {t('detail.noChecklist')}
            </p>
          ) : (
            <div className="ml-6 space-y-1">
              {checklist.map((item) => (
                <div key={item.id} className="flex cursor-pointer items-center gap-2 text-sm">
                  {item.checked ? (
                    <CheckSquare className="h-4 w-4" style={{ color: 'var(--nf-color-accent)' }} />
                  ) : (
                    <Square className="h-4 w-4" style={{ color: 'var(--nf-color-fg-subtle)' }} />
                  )}
                  <span
                    className={item.checked ? 'line-through' : ''}
                    style={{
                      color: item.checked ? 'var(--nf-color-fg-subtle)' : 'var(--nf-color-fg)',
                    }}
                  >
                    {item.text}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Comments */}
        <div>
          <div className="mb-2 flex items-center gap-2">
            <MessageSquare className="h-4 w-4" style={{ color: 'var(--nf-color-fg-subtle)' }} />
            <span className="text-sm font-medium" style={{ color: 'var(--nf-color-fg)' }}>
              {t('detail.comments')} {comments.length > 0 ? `(${comments.length})` : ''}
            </span>
          </div>
          {comments.length === 0 ? (
            <p className="ml-6 text-xs" style={{ color: 'var(--nf-color-fg-subtle)' }}>
              {t('detail.noComments')}
            </p>
          ) : (
            <div className="ml-6 mb-2 space-y-2">
              {comments.map((c) => (
                <div
                  key={c.id}
                  className="rounded p-2"
                  style={{ backgroundColor: 'var(--nf-color-bg-sunken)' }}
                >
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-medium" style={{ color: 'var(--nf-color-fg)' }}>
                      {c.author}
                    </span>
                    <span className="text-[10px]" style={{ color: 'var(--nf-color-fg-subtle)' }}>
                      {DateTime.fromISO(c.createdAt).setLocale(i18n.language).toRelative()}
                    </span>
                  </div>
                  <p className="mt-0.5 text-sm" style={{ color: 'var(--nf-color-fg-muted)' }}>
                    {c.text}
                  </p>
                </div>
              ))}
            </div>
          )}
          <div className="ml-6 flex gap-2">
            <input
              type="text"
              placeholder={t('detail.addComment')}
              value={newComment}
              onChange={(e) => setNewComment(e.target.value)}
              className="flex-1 rounded-md border border-[var(--nf-color-border)] bg-transparent px-2 py-1 text-sm outline-none focus:border-[var(--nf-color-accent)] focus:ring-1 focus:ring-[var(--nf-color-accent)]"
              style={{ color: 'var(--nf-color-fg)' }}
            />
            <button
              type="button"
              disabled={!newComment.trim()}
              className="rounded-md bg-[var(--nf-color-accent)] px-3 py-1 text-sm font-medium text-[var(--nf-color-fg-on-accent)] hover:opacity-80 disabled:opacity-50"
            >
              {t('common.send')}
            </button>
          </div>
        </div>

        {/* Attachments */}
        <div>
          <div className="mb-2 flex items-center gap-2">
            <Paperclip className="h-4 w-4" style={{ color: 'var(--nf-color-fg-subtle)' }} />
            <span className="text-sm font-medium" style={{ color: 'var(--nf-color-fg)' }}>
              {t('detail.attachments')} {attachments.length > 0 ? `(${attachments.length})` : ''}
            </span>
          </div>
          {attachments.length === 0 ? (
            <p className="ml-6 text-xs" style={{ color: 'var(--nf-color-fg-subtle)' }}>
              {t('detail.noAttachments')}
            </p>
          ) : (
            <div className="ml-6 space-y-1">
              {attachments.map((a) => (
                <div
                  key={a.id}
                  className="flex items-center gap-2 rounded px-2 py-1.5 text-sm"
                  style={{ backgroundColor: 'var(--nf-color-bg-sunken)' }}
                >
                  <Paperclip
                    className="h-3.5 w-3.5"
                    style={{ color: 'var(--nf-color-fg-subtle)' }}
                  />
                  <span className="flex-1 truncate">{a.filename}</span>
                  <span className="text-xs" style={{ color: 'var(--nf-color-fg-subtle)' }}>
                    {formatFileSize(a.size)}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Action buttons */}
      <div className="flex gap-2 border-t border-[var(--nf-color-border)] p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] sm:pb-4">
        <button
          type="button"
          onClick={handleEdit}
          className="flex flex-1 items-center justify-center gap-1.5 rounded-md border border-[var(--nf-color-border)] px-3 py-2 text-sm font-medium hover:bg-[var(--nf-color-surface-hover)]"
          style={{ color: 'var(--nf-color-fg)' }}
        >
          <Pencil className="h-4 w-4" />
          {t('common.edit')}
        </button>
        <button
          type="button"
          onClick={handleDelete}
          disabled={deleteMutation.isPending}
          className="flex flex-1 items-center justify-center gap-1.5 rounded-md border px-3 py-2 text-sm font-medium disabled:opacity-50"
          style={
            confirmDelete
              ? { backgroundColor: 'var(--nf-color-danger)', color: '#fff' }
              : { borderColor: 'var(--nf-color-danger)', color: 'var(--nf-color-danger)' }
          }
        >
          <Trash2 className="h-4 w-4" />
          {confirmDelete ? t('common.confirmDelete') : t('common.delete')}
        </button>
      </div>
    </div>
  );

  // Mobile: full-screen overlay; Desktop: side panel
  return (
    <>
      {/* Mobile overlay */}
      <div
        className="fixed inset-0 z-40 sm:hidden"
        style={{ backgroundColor: 'var(--nf-color-bg-elevated)' }}
      >
        {content}
      </div>
      {/* Desktop side panel */}
      <div
        className="hidden w-80 shrink-0 border-l border-[var(--nf-color-border)] sm:flex sm:flex-col"
        style={{ backgroundColor: 'var(--nf-color-bg-elevated)' }}
      >
        {content}
      </div>
    </>
  );
}
