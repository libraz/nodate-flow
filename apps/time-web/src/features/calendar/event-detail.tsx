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

import { useCalendarUiStore } from '../../stores/calendar-ui-store';
import { useCalendarEventsQuery, useDeleteEventMutation } from './api';
import { getEventStyle } from './event-styles';
import type { CalendarEvent, EventKind, Rsvp, ShowAs } from './types';

const KIND_LABELS: Record<EventKind, { label: string; color: string }> = {
  event: { label: 'Event', color: 'bg-blue-100 text-blue-800' },
  block: { label: 'Block', color: 'bg-gray-100 text-gray-800' },
  free: { label: 'Free', color: 'bg-green-100 text-green-800' },
};

const SHOW_AS_LABELS: Record<ShowAs, { label: string; color: string }> = {
  busy: { label: 'Busy', color: 'bg-red-100 text-red-700' },
  free: { label: 'Free', color: 'bg-green-100 text-green-700' },
  tentative: { label: 'Tentative', color: 'bg-yellow-100 text-yellow-700' },
  oof: { label: 'Out of office', color: 'bg-gray-100 text-gray-700' },
};

const RSVP_LABELS: Record<Rsvp, { label: string; color: string }> = {
  pending: { label: 'Pending', color: 'bg-gray-100 text-gray-600' },
  accepted: { label: 'Accepted', color: 'bg-green-100 text-green-700' },
  declined: { label: 'Declined', color: 'bg-red-100 text-red-700' },
  tentative: { label: 'Tentative', color: 'bg-yellow-100 text-yellow-700' },
};

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

function formatDateRange(startAt: string, endAt: string, allDay: boolean): string {
  const start = DateTime.fromISO(startAt);
  const end = DateTime.fromISO(endAt);

  if (allDay) {
    if (start.hasSame(end, 'day') || end.diff(start, 'days').days <= 1) {
      return start.toFormat('cccc, MMMM d, yyyy');
    }
    return `${start.toFormat('MMM d')} - ${end.toFormat('MMM d, yyyy')}`;
  }

  if (start.hasSame(end, 'day')) {
    return `${start.toFormat('cccc, MMMM d, yyyy')}\n${start.toFormat('h:mm a')} - ${end.toFormat('h:mm a')}`;
  }
  return `${start.toFormat('MMM d, h:mm a')} - ${end.toFormat('MMM d, h:mm a, yyyy')}`;
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

export default function EventDetail(): ReactElement | null {
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
      <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 sm:relative sm:inset-auto sm:z-auto sm:w-80 sm:shrink-0 sm:border-l sm:border-gray-200 sm:bg-white">
        <div className="w-full max-w-md rounded-lg bg-white p-6 sm:max-w-none sm:rounded-none sm:shadow-none">
          <p className="text-sm text-gray-500">Loading event...</p>
        </div>
      </div>
    );
  }

  const kindInfo = KIND_LABELS[event.kind];
  const showAsInfo = SHOW_AS_LABELS[event.showAs];
  const eventColor = '#3b82f6';

  const content = (
    <div className="flex h-full flex-col overflow-y-auto">
      {/* Header bar with color */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-gray-200">
        <div className="flex items-center gap-2">
          <div
            className="h-3 w-3 rounded-full shrink-0"
            style={getEventStyle(event.kind, event.showAs, eventColor)}
          />
          <h2 className="text-lg font-semibold truncate">{event.title}</h2>
        </div>
        <button
          type="button"
          onClick={closeEventDetail}
          className="rounded-md p-1 hover:bg-gray-100"
          aria-label="Close detail"
        >
          <X className="h-5 w-5" />
        </button>
      </div>

      <div className="flex-1 space-y-4 p-4">
        {/* Badges */}
        <div className="flex flex-wrap gap-2">
          <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${kindInfo.color}`}>
            {kindInfo.label}
          </span>
          <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${showAsInfo.color}`}>
            {showAsInfo.label}
          </span>
        </div>

        {/* Date/Time */}
        <div className="flex gap-2 text-sm text-gray-700">
          <Clock className="mt-0.5 h-4 w-4 shrink-0 text-gray-400" />
          <span className="whitespace-pre-line">
            {formatDateRange(event.startAt, event.endAt, event.allDay)}
          </span>
        </div>

        {/* Location */}
        {event.location ? (
          <div className="flex gap-2 text-sm text-gray-700">
            <MapPin className="mt-0.5 h-4 w-4 shrink-0 text-gray-400" />
            {isUrl(event.location) ? (
              <a
                href={event.location}
                target="_blank"
                rel="noopener noreferrer"
                className="flex items-center gap-1 text-blue-600 hover:underline"
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
          <div className="rounded-md bg-gray-50 p-3 text-sm text-gray-700 whitespace-pre-wrap">
            {event.memo}
          </div>
        ) : null}

        {/* Owner */}
        <div className="flex items-center gap-2 text-sm">
          <User className="h-4 w-4 text-gray-400" />
          <div className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: eventColor }} />
          <span className="text-gray-700">Owner</span>
        </div>

        {/* Attendees */}
        <div>
          <div className="flex items-center gap-2 mb-2">
            <Users className="h-4 w-4 text-gray-400" />
            <span className="text-sm font-medium text-gray-700">
              Attendees {attendees.length > 0 ? `(${attendees.length})` : ''}
            </span>
          </div>
          {attendees.length === 0 ? (
            <p className="ml-6 text-xs text-gray-400">No attendees</p>
          ) : (
            <div className="ml-6 space-y-1.5">
              {attendees.map((a) => {
                const rsvpInfo = RSVP_LABELS[a.rsvp];
                return (
                  <div key={a.userId} className="flex items-center gap-2">
                    <div
                      className="h-2.5 w-2.5 rounded-full shrink-0"
                      style={{ backgroundColor: a.memberColor }}
                    />
                    <span className="flex-1 truncate text-sm">{a.displayName}</span>
                    <span
                      className={`rounded-full px-1.5 py-0.5 text-[10px] font-medium ${rsvpInfo.color}`}
                    >
                      {rsvpInfo.label}
                    </span>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Checklist */}
        <div>
          <div className="flex items-center gap-2 mb-2">
            <CheckSquare className="h-4 w-4 text-gray-400" />
            <span className="text-sm font-medium text-gray-700">Checklist</span>
          </div>
          {checklist.length === 0 ? (
            <p className="ml-6 text-xs text-gray-400">No checklist items</p>
          ) : (
            <div className="ml-6 space-y-1">
              {checklist.map((item) => (
                <div key={item.id} className="flex items-center gap-2 text-sm cursor-pointer">
                  {item.checked ? (
                    <CheckSquare className="h-4 w-4 text-blue-500" />
                  ) : (
                    <Square className="h-4 w-4 text-gray-300" />
                  )}
                  <span className={item.checked ? 'text-gray-400 line-through' : ''}>
                    {item.text}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Comments */}
        <div>
          <div className="flex items-center gap-2 mb-2">
            <MessageSquare className="h-4 w-4 text-gray-400" />
            <span className="text-sm font-medium text-gray-700">
              Comments {comments.length > 0 ? `(${comments.length})` : ''}
            </span>
          </div>
          {comments.length === 0 ? (
            <p className="ml-6 text-xs text-gray-400">No comments yet</p>
          ) : (
            <div className="ml-6 space-y-2 mb-2">
              {comments.map((c) => (
                <div key={c.id} className="rounded bg-gray-50 p-2">
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-medium text-gray-700">{c.author}</span>
                    <span className="text-[10px] text-gray-400">
                      {DateTime.fromISO(c.createdAt).toRelative()}
                    </span>
                  </div>
                  <p className="mt-0.5 text-sm text-gray-600">{c.text}</p>
                </div>
              ))}
            </div>
          )}
          <div className="ml-6 flex gap-2">
            <input
              type="text"
              placeholder="Add a comment..."
              value={newComment}
              onChange={(e) => setNewComment(e.target.value)}
              className="flex-1 rounded-md border border-gray-300 px-2 py-1 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <button
              type="button"
              disabled={!newComment.trim()}
              className="rounded-md bg-blue-600 px-3 py-1 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            >
              Send
            </button>
          </div>
        </div>

        {/* Attachments */}
        <div>
          <div className="flex items-center gap-2 mb-2">
            <Paperclip className="h-4 w-4 text-gray-400" />
            <span className="text-sm font-medium text-gray-700">
              Attachments {attachments.length > 0 ? `(${attachments.length})` : ''}
            </span>
          </div>
          {attachments.length === 0 ? (
            <p className="ml-6 text-xs text-gray-400">No attachments</p>
          ) : (
            <div className="ml-6 space-y-1">
              {attachments.map((a) => (
                <div
                  key={a.id}
                  className="flex items-center gap-2 rounded bg-gray-50 px-2 py-1.5 text-sm"
                >
                  <Paperclip className="h-3.5 w-3.5 text-gray-400" />
                  <span className="flex-1 truncate">{a.filename}</span>
                  <span className="text-xs text-gray-400">{formatFileSize(a.size)}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Action buttons */}
      <div className="flex gap-2 border-t border-gray-200 p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] sm:pb-4">
        <button
          type="button"
          onClick={handleEdit}
          className="flex flex-1 items-center justify-center gap-1.5 rounded-md border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
        >
          <Pencil className="h-4 w-4" />
          Edit
        </button>
        <button
          type="button"
          onClick={handleDelete}
          disabled={deleteMutation.isPending}
          className={`flex flex-1 items-center justify-center gap-1.5 rounded-md px-3 py-2 text-sm font-medium ${
            confirmDelete
              ? 'bg-red-600 text-white hover:bg-red-700'
              : 'border border-red-300 text-red-600 hover:bg-red-50'
          } disabled:opacity-50`}
        >
          <Trash2 className="h-4 w-4" />
          {confirmDelete ? 'Confirm Delete' : 'Delete'}
        </button>
      </div>
    </div>
  );

  // Mobile: full-screen overlay; Desktop: side panel
  return (
    <>
      {/* Mobile overlay */}
      <div className="fixed inset-0 z-40 bg-white sm:hidden">{content}</div>
      {/* Desktop side panel */}
      <div className="hidden w-80 shrink-0 border-l border-gray-200 bg-white sm:flex sm:flex-col">
        {content}
      </div>
    </>
  );
}
