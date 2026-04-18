import { useQuery } from '@tanstack/react-query';
import { Link, createFileRoute } from '@tanstack/react-router';
import { Calendar as CalendarIcon } from 'lucide-react';
import { DateTime } from 'luxon';
import type { ReactElement } from 'react';

import { useAcceptInviteMutation } from '../../features/calendar/api';
import { api } from '../../lib/api-client';
import { useAuthStore } from '../../stores/auth-store';

interface SharedCalendar {
  id: string;
  name: string;
  color: string;
}

interface SharedEvent {
  id: string;
  title: string;
  allDay: boolean;
  startAt: string;
  endAt: string;
}

function useShareCalendarQuery(token: string) {
  return useQuery({
    queryKey: ['share', token, 'calendar'],
    queryFn: () => api.get<SharedCalendar>(`/share/${token}`),
  });
}

function useShareEventsQuery(token: string) {
  return useQuery({
    queryKey: ['share', token, 'events'],
    queryFn: () =>
      api.get<{ events: SharedEvent[] }>(`/share/${token}/events`).then((r) => r.events),
  });
}

export const Route = createFileRoute('/share/$token')({
  component: SharePage,
});

function SharePage(): ReactElement {
  const { token } = Route.useParams();
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const { data: calendar, isLoading: calLoading, error: calError } = useShareCalendarQuery(token);
  const { data: events, isLoading: eventsLoading } = useShareEventsQuery(token);
  const acceptMutation = useAcceptInviteMutation();

  if (calLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <p className="text-gray-500">Loading shared calendar...</p>
      </div>
    );
  }

  if (calError || !calendar) {
    return (
      <div className="flex min-h-screen flex-col items-center justify-center gap-4">
        <CalendarIcon className="h-12 w-12 text-gray-300" />
        <p className="text-gray-500">This shared calendar link is invalid or has expired.</p>
        <Link to="/login" className="text-sm text-blue-600 hover:underline">
          Sign in
        </Link>
      </div>
    );
  }

  const handleJoin = () => {
    acceptMutation.mutate(token, {
      onSuccess: () => {
        window.location.href = '/calendar';
      },
    });
  };

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="border-b border-gray-200 bg-white px-4 py-4">
        <div className="mx-auto flex max-w-3xl items-center justify-between">
          <div className="flex items-center gap-3">
            <div
              className="h-4 w-4 rounded-full"
              style={{ backgroundColor: calendar.color || '#3b82f6' }}
            />
            <h1 className="text-lg font-semibold">{calendar.name}</h1>
          </div>
          <div className="flex items-center gap-2">
            {isAuthenticated ? (
              <button
                type="button"
                onClick={handleJoin}
                disabled={acceptMutation.isPending}
                className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
              >
                {acceptMutation.isPending ? 'Joining...' : 'Join Calendar'}
              </button>
            ) : (
              <Link
                to="/login"
                className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
              >
                Sign in to join
              </Link>
            )}
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-3xl px-4 py-6">
        {acceptMutation.isSuccess ? (
          <div className="mb-4 rounded-md bg-green-50 px-4 py-3 text-sm text-green-700">
            Successfully joined the calendar! Redirecting...
          </div>
        ) : null}

        {acceptMutation.isError ? (
          <div className="mb-4 rounded-md bg-red-50 px-4 py-3 text-sm text-red-700">
            {acceptMutation.error.message}
          </div>
        ) : null}

        {eventsLoading ? (
          <div className="space-y-3">
            {['sk-1', 'sk-2', 'sk-3'].map((id) => (
              <div key={id} className="h-16 animate-pulse rounded-md bg-gray-100" />
            ))}
          </div>
        ) : events?.length === 0 ? (
          <p className="py-12 text-center text-gray-400">No upcoming events</p>
        ) : (
          <div className="space-y-2">
            {events?.map((event) => {
              const start = DateTime.fromISO(event.startAt);
              const end = DateTime.fromISO(event.endAt);
              return (
                <div
                  key={event.id}
                  className="rounded-md border border-gray-200 bg-white px-4 py-3"
                >
                  <p className="font-medium text-gray-900">{event.title}</p>
                  <p className="mt-1 text-sm text-gray-500">
                    {event.allDay
                      ? start.toLocaleString(DateTime.DATE_MED)
                      : `${start.toLocaleString(DateTime.DATETIME_MED)} - ${end.toLocaleString(DateTime.TIME_SIMPLE)}`}
                  </p>
                </div>
              );
            })}
          </div>
        )}
      </main>
    </div>
  );
}
