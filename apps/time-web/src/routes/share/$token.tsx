import { useQuery } from '@tanstack/react-query';
import { Link, createFileRoute } from '@tanstack/react-router';
import { Calendar as CalendarIcon } from 'lucide-react';
import { DateTime } from 'luxon';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';

import { selectIsAuthenticated, useAuth } from '../../features/auth/auth-store';
import { useAcceptInviteMutation } from '../../features/share/api';
import { toApiError } from '../../lib/api-error';
import { flowWebUrl, sdk } from '../../lib/sdk';

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
    queryFn: async () => {
      const result = await sdk.GET('/share/{token}', {
        params: { path: { token } },
      });
      if (result.error || !result.data) {
        throw toApiError(result.error, 'Failed to fetch shared calendar');
      }
      return result.data as SharedCalendar;
    },
  });
}

function useShareEventsQuery(token: string) {
  return useQuery({
    queryKey: ['share', token, 'events'],
    queryFn: async () => {
      const result = await sdk.GET('/share/{token}/events', {
        params: { path: { token } },
      });
      if (result.error || !result.data) {
        throw toApiError(result.error, 'Failed to fetch shared events');
      }
      const body = result.data as { events: SharedEvent[] };
      return body.events;
    },
  });
}

export const Route = createFileRoute('/share/$token')({
  component: SharePage,
});

function SharePage(): ReactElement {
  const { token } = Route.useParams();
  const { t } = useTranslation();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const { data: calendar, isLoading: calLoading, error: calError } = useShareCalendarQuery(token);
  const { data: events, isLoading: eventsLoading } = useShareEventsQuery(token);
  const acceptMutation = useAcceptInviteMutation();

  if (calLoading) {
    return (
      <div
        style={{
          display: 'flex',
          minHeight: '100vh',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <p style={{ color: 'var(--nf-color-fg-muted)' }}>{t('share.loading')}</p>
      </div>
    );
  }

  if (calError || !calendar) {
    return (
      <div
        style={{
          display: 'flex',
          minHeight: '100vh',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          gap: 'var(--nf-space-4)',
        }}
      >
        <CalendarIcon size={48} style={{ color: 'var(--nf-color-fg-subtle)' }} />
        <p style={{ color: 'var(--nf-color-fg-muted)' }}>{t('share.invalid_or_expired')}</p>
        <Link
          to="/login"
          style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-accent)' }}
        >
          {t('share.sign_in')}
        </Link>
      </div>
    );
  }

  const handleJoin = () => {
    acceptMutation.mutate(token, {
      onSuccess: () => {
        window.location.href = `${flowWebUrl}/calendar`;
      },
    });
  };

  return (
    <div style={{ minHeight: '100vh', backgroundColor: 'var(--nf-color-bg)' }}>
      <header
        style={{
          borderBlockEnd: '1px solid var(--nf-color-border)',
          backgroundColor: 'var(--nf-color-bg-elevated)',
          padding: 'var(--nf-space-4)',
        }}
      >
        <div
          style={{
            maxWidth: '48rem',
            marginInline: 'auto',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-3)' }}>
            <div
              style={{
                width: '1rem',
                height: '1rem',
                borderRadius: 'var(--nf-radius-pill)',
                backgroundColor: calendar.color || 'var(--nf-color-accent)',
              }}
            />
            <h1 style={{ fontSize: 'var(--nf-text-lg)', fontWeight: 'var(--nf-weight-semibold)' }}>
              {calendar.name}
            </h1>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}>
            {isAuthenticated ? (
              <Button
                variant="primary"
                size="sm"
                onClick={handleJoin}
                disabled={acceptMutation.isPending}
              >
                {acceptMutation.isPending ? t('share.joining') : t('share.join_calendar')}
              </Button>
            ) : (
              <Link
                to="/login"
                style={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  borderRadius: 'var(--nf-radius-md)',
                  backgroundColor: 'var(--nf-color-accent)',
                  color: 'var(--nf-color-fg-on-accent)',
                  padding: 'var(--nf-space-2) var(--nf-space-4)',
                  fontSize: 'var(--nf-text-sm)',
                  fontWeight: 'var(--nf-weight-medium)',
                  textDecoration: 'none',
                }}
              >
                {t('share.sign_in_to_join')}
              </Link>
            )}
          </div>
        </div>
      </header>

      <main
        style={{
          maxWidth: '48rem',
          marginInline: 'auto',
          padding: 'var(--nf-space-6) var(--nf-space-4)',
        }}
      >
        {acceptMutation.isSuccess ? (
          <div
            style={{
              marginBlockEnd: 'var(--nf-space-4)',
              borderRadius: 'var(--nf-radius-md)',
              backgroundColor: 'var(--nf-color-success-subtle)',
              color: 'var(--nf-color-success)',
              padding: 'var(--nf-space-3) var(--nf-space-4)',
              fontSize: 'var(--nf-text-sm)',
            }}
          >
            {t('share.join_success')}
          </div>
        ) : null}

        {acceptMutation.isError ? (
          <div
            style={{
              marginBlockEnd: 'var(--nf-space-4)',
              borderRadius: 'var(--nf-radius-md)',
              backgroundColor: 'var(--nf-color-danger-subtle)',
              color: 'var(--nf-color-danger)',
              padding: 'var(--nf-space-3) var(--nf-space-4)',
              fontSize: 'var(--nf-text-sm)',
            }}
          >
            {acceptMutation.error.message}
          </div>
        ) : null}

        {eventsLoading ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3)' }}>
            <Skeleton style={{ height: '4rem' }} />
            <Skeleton style={{ height: '4rem' }} />
            <Skeleton style={{ height: '4rem' }} />
          </div>
        ) : events?.length === 0 ? (
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
            {events?.map((event) => {
              const start = DateTime.fromISO(event.startAt);
              const end = DateTime.fromISO(event.endAt);
              return (
                <Card key={event.id}>
                  <p
                    style={{
                      fontWeight: 'var(--nf-weight-medium)',
                      color: 'var(--nf-color-fg)',
                    }}
                  >
                    {event.title}
                  </p>
                  <p
                    style={{
                      marginBlockStart: 'var(--nf-space-1)',
                      fontSize: 'var(--nf-text-sm)',
                      color: 'var(--nf-color-fg-muted)',
                    }}
                  >
                    {event.allDay
                      ? start.toLocaleString(DateTime.DATE_MED)
                      : `${start.toLocaleString(DateTime.DATETIME_MED)} - ${end.toLocaleString(DateTime.TIME_SIMPLE)}`}
                  </p>
                </Card>
              );
            })}
          </div>
        )}
      </main>
    </div>
  );
}
