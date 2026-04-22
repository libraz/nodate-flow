/**
 * /me/invites — authenticated inbox listing pending event invites
 * addressed to the caller's email.
 *
 * This page is intentionally read-only: the canonical RSVP flow is
 * through the magic-link email + /invites/accept page, which carries
 * the plaintext token required by the public accept endpoint. The
 * list response here does not expose the plaintext token (only a
 * public id) so this page acts as a summary inbox.
 */

import { useQuery } from '@tanstack/react-query';
import { Navigate, createFileRoute } from '@tanstack/react-router';
import { Calendar as CalendarIcon, Clock, MapPin } from 'lucide-react';
import { DateTime } from 'luxon';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import Card from '@nodate-flow/ui/primitives/card';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';

import { selectIsAuthenticated, useAuth } from '../../features/auth/auth-store';
import { useAuthBootstrap } from '../../features/auth/use-auth-bootstrap';
import { ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

interface MyInvite {
  id: string;
  eventPublicId: string;
  eventTitle: string;
  eventAllDay: boolean;
  eventStartAt?: number;
  eventEndAt?: number;
  eventLocation?: string;
  calendarPublicId: string;
  calendarName: string;
  workspacePublicId: string;
  workspaceName: string;
  createdAt: number;
  expiresAt: number;
}

interface ListMyInvitesBody {
  invites: MyInvite[] | null;
}

function useMyInvitesQuery(enabled: boolean) {
  return useQuery({
    queryKey: ['me', 'invites'],
    enabled,
    queryFn: async () => {
      const result = await sdk.GET('/me/invites');
      if (result.error || !result.data) {
        throw toApiError(result.error, 'Failed to load invites');
      }
      return result.data as ListMyInvitesBody;
    },
  });
}

export const Route = createFileRoute('/me/invites')({
  component: MyInvitesPage,
});

function MyInvitesPage(): ReactElement | null {
  const { status } = useAuthBootstrap();
  const isAuthenticated = useAuth(selectIsAuthenticated);

  if (status === 'loading') {
    return <InvitesLoading />;
  }
  if (!isAuthenticated) {
    return <Navigate to="/login" />;
  }
  return <MyInvitesContent />;
}

function MyInvitesContent(): ReactElement {
  const { t } = useTranslation();
  const { data, isLoading, error } = useMyInvitesQuery(true);

  if (isLoading) return <InvitesLoading />;

  if (error) {
    const message = error instanceof ApiError ? error.message : t('invites.inbox.load_error');
    return (
      <PageShell>
        <header style={headerStyle}>
          <h1 style={headingStyle}>{t('invites.inbox.title')}</h1>
        </header>
        <Card>
          <p style={{ margin: 0, color: 'var(--nf-color-danger)' }}>{message}</p>
        </Card>
      </PageShell>
    );
  }

  const invites = data?.invites ?? [];

  return (
    <PageShell>
      <header style={headerStyle}>
        <h1 style={headingStyle}>{t('invites.inbox.title')}</h1>
        <p style={subheadingStyle}>{t('invites.inbox.rsvp_hint')}</p>
      </header>

      {invites.length === 0 ? (
        <Card>
          <p
            style={{
              margin: 0,
              textAlign: 'center',
              color: 'var(--nf-color-fg-muted)',
              padding: 'var(--nf-space-6) 0',
            }}
          >
            {t('invites.inbox.empty')}
          </p>
        </Card>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
          {invites.map((invite) => (
            <InviteCard key={invite.id} invite={invite} />
          ))}
        </div>
      )}
    </PageShell>
  );
}

interface InviteCardProps {
  invite: MyInvite;
}

function InviteCard({ invite }: InviteCardProps): ReactElement {
  const { t, i18n } = useTranslation();
  const locale = i18n.language;
  const zone = DateTime.local().zoneName ?? 'UTC';

  const start = invite.eventStartAt ? DateTime.fromSeconds(invite.eventStartAt, { zone }) : null;
  const end = invite.eventEndAt ? DateTime.fromSeconds(invite.eventEndAt, { zone }) : null;

  let whenLabel: string;
  if (!start) {
    whenLabel = t('invites.inbox.undated');
  } else if (invite.eventAllDay) {
    whenLabel = start.setLocale(locale).toLocaleString(DateTime.DATE_MED);
  } else if (end?.hasSame(start, 'day')) {
    whenLabel = `${start.setLocale(locale).toLocaleString(DateTime.DATETIME_MED)} – ${end
      .setLocale(locale)
      .toLocaleString(DateTime.TIME_SIMPLE)}`;
  } else if (end) {
    whenLabel = `${start.setLocale(locale).toLocaleString(DateTime.DATETIME_MED)} – ${end
      .setLocale(locale)
      .toLocaleString(DateTime.DATETIME_MED)}`;
  } else {
    whenLabel = start.setLocale(locale).toLocaleString(DateTime.DATETIME_MED);
  }

  const expires = DateTime.fromSeconds(invite.expiresAt).setLocale(locale);
  const totalHours = expires.diff(DateTime.now(), ['hours', 'minutes']).as('hours');
  let expiresLabel: string;
  if (totalHours <= 0) {
    expiresLabel = t('invites.inbox.expired');
  } else if (totalHours < 24) {
    expiresLabel = t('invites.inbox.expires_in_hours', {
      count: Math.max(1, Math.round(totalHours)),
    });
  } else {
    expiresLabel = t('invites.inbox.expires_at', {
      date: expires.toLocaleString(DateTime.DATETIME_MED),
    });
  }

  return (
    <Card>
      <p
        style={{
          margin: 0,
          fontWeight: 'var(--nf-weight-semibold)',
          color: 'var(--nf-color-fg)',
          fontSize: 'var(--nf-text-base)',
        }}
      >
        {invite.eventTitle}
      </p>

      <p
        style={{
          marginBlockStart: 'var(--nf-space-1)',
          marginBlockEnd: 0,
          fontSize: 'var(--nf-text-sm)',
          color: 'var(--nf-color-fg-muted)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--nf-space-1)',
        }}
      >
        <Clock size={14} aria-hidden="true" />
        {whenLabel}
      </p>

      {invite.eventLocation ? (
        <p
          style={{
            marginBlockStart: 'var(--nf-space-1)',
            marginBlockEnd: 0,
            fontSize: 'var(--nf-text-sm)',
            color: 'var(--nf-color-fg-muted)',
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--nf-space-1)',
          }}
        >
          <MapPin size={14} aria-hidden="true" />
          {invite.eventLocation}
        </p>
      ) : null}

      <p
        style={{
          marginBlockStart: 'var(--nf-space-2)',
          marginBlockEnd: 0,
          fontSize: 'var(--nf-text-xs)',
          color: 'var(--nf-color-fg-subtle)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--nf-space-1)',
        }}
      >
        <CalendarIcon size={12} aria-hidden="true" />
        {t('invites.inbox.calendar_workspace', {
          calendar: invite.calendarName,
          workspace: invite.workspaceName,
        })}
      </p>

      <p
        style={{
          marginBlockStart: 'var(--nf-space-1)',
          marginBlockEnd: 0,
          fontSize: 'var(--nf-text-xs)',
          color: 'var(--nf-color-fg-subtle)',
        }}
      >
        {expiresLabel}
      </p>
    </Card>
  );
}

function PageShell({ children }: { children: React.ReactNode }): ReactElement {
  return (
    <main
      style={{
        maxWidth: '42rem',
        marginInline: 'auto',
        padding: 'var(--nf-space-6) var(--nf-space-4)',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-4)',
        minBlockSize: '100vh',
      }}
    >
      {children}
    </main>
  );
}

function InvitesLoading(): ReactElement {
  const { t } = useTranslation();
  return (
    <PageShell>
      <header style={headerStyle}>
        <h1 style={headingStyle}>{t('invites.inbox.title')}</h1>
      </header>
      <Skeleton style={{ height: '6rem' }} />
      <Skeleton style={{ height: '6rem' }} />
      <Skeleton style={{ height: '6rem' }} />
    </PageShell>
  );
}

const headerStyle: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--nf-space-1)',
};

const headingStyle: React.CSSProperties = {
  margin: 0,
  fontFamily: 'var(--font-display)',
  fontSize: 'var(--nf-text-2xl)',
  fontWeight: 'var(--nf-weight-bold)',
  color: 'var(--nf-color-fg)',
};

const subheadingStyle: React.CSSProperties = {
  margin: 0,
  fontSize: 'var(--nf-text-sm)',
  color: 'var(--nf-color-fg-muted)',
};
