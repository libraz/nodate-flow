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

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Input from '@nodate-flow/ui/primitives/input';

import { useCalendarUi } from '../../stores/calendar-ui-store';
import { useCalendarEventsQuery, useDeleteEventMutation } from './api';
import detailStyles from './event-detail.module.css';
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
  const selectedDate = useCalendarUi((s) => s.selectedDate);
  const currentView = useCalendarUi((s) => s.currentView);

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

type BadgeToneType = 'accent' | 'neutral';

function kindTone(kind: EventKind): BadgeToneType {
  return kind === 'event' ? 'accent' : 'neutral';
}

function showAsTone(showAs: ShowAs): BadgeToneType {
  return showAs === 'busy' ? 'accent' : 'neutral';
}

function rsvpTone(rsvp: Rsvp): BadgeToneType {
  return rsvp === 'accepted' ? 'accent' : 'neutral';
}

export default function EventDetail(): ReactElement | null {
  const { t, i18n } = useTranslation();
  const eventDetailId = useCalendarUi((s) => s.eventDetailId);
  const closeEventDetail = useCalendarUi((s) => s.closeEventDetail);
  const openEventModal = useCalendarUi((s) => s.openEventModal);
  const deleteMutation = useDeleteEventMutation();
  const [confirmDelete, setConfirmDelete] = useState(false);

  // Stubs for features with empty data
  const attendees: Attendee[] = [];
  const checklist: ChecklistItem[] = [];
  const comments: Comment[] = [];
  const attachments: Attachment[] = [];
  const [newComment, setNewComment] = useState('');

  const event = useEventById(eventDetailId ?? '');

  const kindLabels: Record<EventKind, string> = {
    event: t('event.kindEvent'),
    block: t('event.kindBlock'),
    free: t('event.kindFree'),
  };

  const showAsLabels: Record<ShowAs, string> = {
    busy: t('event.showBusy'),
    free: t('event.showFree'),
    tentative: t('event.showTentative'),
    oof: t('event.showOof'),
  };

  const rsvpLabels: Record<Rsvp, string> = {
    pending: t('rsvp.pending'),
    accepted: t('rsvp.accepted'),
    declined: t('rsvp.declined'),
    tentative: t('rsvp.tentative'),
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
      <div className={detailStyles.loadingOverlay}>
        <div className={detailStyles.loadingCard}>
          <p style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}>
            {t('event.loadingEvent')}
          </p>
        </div>
      </div>
    );
  }

  const eventColor = '#3b82f6';

  const content = (
    <div className={detailStyles.scrollContainer}>
      {/* Header bar */}
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          borderBlockEnd: '1px solid var(--nf-color-border)',
          padding: 'var(--nf-space-3) var(--nf-space-4)',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}>
          <div
            style={{
              width: '0.75rem',
              height: '0.75rem',
              flexShrink: 0,
              borderRadius: 'var(--nf-radius-pill)',
              ...getEventStyle(event.kind, event.showAs, eventColor),
            }}
          />
          <h2
            style={{
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
              fontSize: 'var(--nf-text-lg)',
              fontWeight: 'var(--nf-weight-semibold)',
            }}
          >
            {event.title}
          </h2>
        </div>
        <Button variant="ghost" size="sm" onClick={closeEventDetail} aria-label={t('common.close')}>
          <X size={20} />
        </Button>
      </div>

      <div
        style={{
          flex: 1,
          padding: 'var(--nf-space-4)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-4)',
        }}
      >
        {/* Badges */}
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--nf-space-2)' }}>
          <Badge tone={kindTone(event.kind)}>{kindLabels[event.kind]}</Badge>
          <Badge tone={showAsTone(event.showAs)}>{showAsLabels[event.showAs]}</Badge>
        </div>

        {/* Date/Time */}
        <div
          style={{
            display: 'flex',
            gap: 'var(--nf-space-2)',
            fontSize: 'var(--nf-text-sm)',
            color: 'var(--nf-color-fg)',
          }}
        >
          <Clock
            size={16}
            style={{
              marginBlockStart: '0.125rem',
              flexShrink: 0,
              color: 'var(--nf-color-fg-subtle)',
            }}
          />
          <span style={{ whiteSpace: 'pre-line' }}>
            {formatDateRange(event.startAt, event.endAt, event.allDay, i18n.language)}
          </span>
        </div>

        {/* Location */}
        {event.location ? (
          <div
            style={{
              display: 'flex',
              gap: 'var(--nf-space-2)',
              fontSize: 'var(--nf-text-sm)',
              color: 'var(--nf-color-fg)',
            }}
          >
            <MapPin
              size={16}
              style={{
                marginBlockStart: '0.125rem',
                flexShrink: 0,
                color: 'var(--nf-color-fg-subtle)',
              }}
            />
            {isUrl(event.location) ? (
              <a
                href={event.location}
                target="_blank"
                rel="noopener noreferrer"
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--nf-space-1)',
                  color: 'var(--nf-color-accent)',
                }}
              >
                {event.location}
                <ExternalLink size={12} />
              </a>
            ) : (
              <span>{event.location}</span>
            )}
          </div>
        ) : null}

        {/* Memo */}
        {event.memo ? (
          <Card>
            <p
              style={{
                fontSize: 'var(--nf-text-sm)',
                whiteSpace: 'pre-wrap',
                color: 'var(--nf-color-fg)',
              }}
            >
              {event.memo}
            </p>
          </Card>
        ) : null}

        {/* Owner */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--nf-space-2)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          <User size={16} style={{ color: 'var(--nf-color-fg-subtle)' }} />
          <div
            style={{
              width: '0.625rem',
              height: '0.625rem',
              borderRadius: 'var(--nf-radius-pill)',
              backgroundColor: eventColor,
            }}
          />
          <span style={{ color: 'var(--nf-color-fg)' }}>{t('common.owner')}</span>
        </div>

        {/* Attendees */}
        <div>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--nf-space-2)',
              marginBlockEnd: 'var(--nf-space-2)',
            }}
          >
            <Users size={16} style={{ color: 'var(--nf-color-fg-subtle)' }} />
            <span
              style={{
                fontSize: 'var(--nf-text-sm)',
                fontWeight: 'var(--nf-weight-medium)',
                color: 'var(--nf-color-fg)',
              }}
            >
              {t('detail.attendees')} {attendees.length > 0 ? `(${attendees.length})` : ''}
            </span>
          </div>
          {attendees.length === 0 ? (
            <p
              style={{
                marginInlineStart: 'var(--nf-space-6)',
                fontSize: 'var(--nf-text-xs)',
                color: 'var(--nf-color-fg-subtle)',
              }}
            >
              {t('detail.noAttendees')}
            </p>
          ) : (
            <div
              style={{
                marginInlineStart: 'var(--nf-space-6)',
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--nf-space-1)',
              }}
            >
              {attendees.map((a) => (
                <div
                  key={a.userId}
                  style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}
                >
                  <div
                    style={{
                      width: '0.625rem',
                      height: '0.625rem',
                      flexShrink: 0,
                      borderRadius: 'var(--nf-radius-pill)',
                      backgroundColor: a.memberColor,
                    }}
                  />
                  <span
                    style={{
                      flex: 1,
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                      fontSize: 'var(--nf-text-sm)',
                    }}
                  >
                    {a.displayName}
                  </span>
                  <Badge tone={rsvpTone(a.rsvp)}>{rsvpLabels[a.rsvp]}</Badge>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Checklist */}
        <div>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--nf-space-2)',
              marginBlockEnd: 'var(--nf-space-2)',
            }}
          >
            <CheckSquare size={16} style={{ color: 'var(--nf-color-fg-subtle)' }} />
            <span
              style={{
                fontSize: 'var(--nf-text-sm)',
                fontWeight: 'var(--nf-weight-medium)',
                color: 'var(--nf-color-fg)',
              }}
            >
              {t('detail.checklist')}
            </span>
          </div>
          {checklist.length === 0 ? (
            <p
              style={{
                marginInlineStart: 'var(--nf-space-6)',
                fontSize: 'var(--nf-text-xs)',
                color: 'var(--nf-color-fg-subtle)',
              }}
            >
              {t('detail.noChecklist')}
            </p>
          ) : (
            <div
              style={{
                marginInlineStart: 'var(--nf-space-6)',
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--nf-space-1)',
              }}
            >
              {checklist.map((item) => (
                <div
                  key={item.id}
                  style={{
                    display: 'flex',
                    cursor: 'pointer',
                    alignItems: 'center',
                    gap: 'var(--nf-space-2)',
                    fontSize: 'var(--nf-text-sm)',
                  }}
                >
                  {item.checked ? (
                    <CheckSquare size={16} style={{ color: 'var(--nf-color-accent)' }} />
                  ) : (
                    <Square size={16} style={{ color: 'var(--nf-color-fg-subtle)' }} />
                  )}
                  <span
                    style={{
                      textDecoration: item.checked ? 'line-through' : 'none',
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
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--nf-space-2)',
              marginBlockEnd: 'var(--nf-space-2)',
            }}
          >
            <MessageSquare size={16} style={{ color: 'var(--nf-color-fg-subtle)' }} />
            <span
              style={{
                fontSize: 'var(--nf-text-sm)',
                fontWeight: 'var(--nf-weight-medium)',
                color: 'var(--nf-color-fg)',
              }}
            >
              {t('detail.comments')} {comments.length > 0 ? `(${comments.length})` : ''}
            </span>
          </div>
          {comments.length === 0 ? (
            <p
              style={{
                marginInlineStart: 'var(--nf-space-6)',
                fontSize: 'var(--nf-text-xs)',
                color: 'var(--nf-color-fg-subtle)',
              }}
            >
              {t('detail.noComments')}
            </p>
          ) : (
            <div
              style={{
                marginInlineStart: 'var(--nf-space-6)',
                marginBlockEnd: 'var(--nf-space-2)',
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--nf-space-2)',
              }}
            >
              {comments.map((c) => (
                <Card key={c.id}>
                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                    }}
                  >
                    <span
                      style={{
                        fontSize: 'var(--nf-text-xs)',
                        fontWeight: 'var(--nf-weight-medium)',
                        color: 'var(--nf-color-fg)',
                      }}
                    >
                      {c.author}
                    </span>
                    <span style={{ fontSize: '10px', color: 'var(--nf-color-fg-subtle)' }}>
                      {DateTime.fromISO(c.createdAt).setLocale(i18n.language).toRelative()}
                    </span>
                  </div>
                  <p
                    style={{
                      marginBlockStart: 'var(--nf-space-1)',
                      fontSize: 'var(--nf-text-sm)',
                      color: 'var(--nf-color-fg-muted)',
                    }}
                  >
                    {c.text}
                  </p>
                </Card>
              ))}
            </div>
          )}
          <div
            style={{
              marginInlineStart: 'var(--nf-space-6)',
              display: 'flex',
              gap: 'var(--nf-space-2)',
            }}
          >
            <Input
              type="text"
              placeholder={t('detail.addComment')}
              value={newComment}
              onChange={(e) => setNewComment(e.target.value)}
              style={{ flex: 1 }}
            />
            <Button variant="primary" size="sm" disabled={!newComment.trim()}>
              {t('common.send')}
            </Button>
          </div>
        </div>

        {/* Attachments */}
        <div>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--nf-space-2)',
              marginBlockEnd: 'var(--nf-space-2)',
            }}
          >
            <Paperclip size={16} style={{ color: 'var(--nf-color-fg-subtle)' }} />
            <span
              style={{
                fontSize: 'var(--nf-text-sm)',
                fontWeight: 'var(--nf-weight-medium)',
                color: 'var(--nf-color-fg)',
              }}
            >
              {t('detail.attachments')} {attachments.length > 0 ? `(${attachments.length})` : ''}
            </span>
          </div>
          {attachments.length === 0 ? (
            <p
              style={{
                marginInlineStart: 'var(--nf-space-6)',
                fontSize: 'var(--nf-text-xs)',
                color: 'var(--nf-color-fg-subtle)',
              }}
            >
              {t('detail.noAttachments')}
            </p>
          ) : (
            <div
              style={{
                marginInlineStart: 'var(--nf-space-6)',
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--nf-space-1)',
              }}
            >
              {attachments.map((a) => (
                <Card key={a.id}>
                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 'var(--nf-space-2)',
                      fontSize: 'var(--nf-text-sm)',
                    }}
                  >
                    <Paperclip size={14} style={{ color: 'var(--nf-color-fg-subtle)' }} />
                    <span
                      style={{
                        flex: 1,
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {a.filename}
                    </span>
                    <span
                      style={{ fontSize: 'var(--nf-text-xs)', color: 'var(--nf-color-fg-subtle)' }}
                    >
                      {formatFileSize(a.size)}
                    </span>
                  </div>
                </Card>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Action buttons */}
      <div
        style={{
          display: 'flex',
          gap: 'var(--nf-space-2)',
          borderBlockStart: '1px solid var(--nf-color-border)',
          padding: 'var(--nf-space-4)',
          paddingBlockEnd: 'calc(var(--nf-space-4) + env(safe-area-inset-bottom))',
        }}
      >
        <Button variant="default" onClick={handleEdit} style={{ flex: 1 }}>
          <Pencil size={16} />
          {t('common.edit')}
        </Button>
        <Button
          variant={confirmDelete ? 'danger' : 'default'}
          onClick={handleDelete}
          disabled={deleteMutation.isPending}
          style={{ flex: 1 }}
        >
          <Trash2 size={16} />
          {confirmDelete ? t('common.confirmDelete') : t('common.delete')}
        </Button>
      </div>
    </div>
  );

  // Mobile: full-screen overlay; Desktop: side panel
  return (
    <>
      {/* Mobile overlay */}
      <div className={detailStyles.mobileOverlay}>{content}</div>
      {/* Desktop side panel */}
      <div className={detailStyles.desktopPanel}>{content}</div>
    </>
  );
}
