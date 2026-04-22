/**
 * /invites/accept?token=XXX — public magic-link RSVP page.
 *
 * Unauthenticated: consumes the plaintext magic-link token from the
 * invite email and lets the recipient record an RSVP without signing
 * in. The backend response is minimal (just inviteId + rsvp), so this
 * page shows a simple confirmation without event enrichment.
 */

import { useMutation } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { CalendarCheck, CircleAlert } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';

import { ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

type RsvpChoice = 'accepted' | 'tentative' | 'declined';

export interface AcceptSearch {
  token?: string;
}

interface AcceptInviteResult {
  inviteId: string;
  rsvp: string;
}

export const Route = createFileRoute('/invites/accept')({
  validateSearch: (search: Record<string, unknown>): AcceptSearch => {
    const token = typeof search.token === 'string' ? search.token : undefined;
    return token ? { token } : {};
  },
  component: AcceptInvitePage,
});

function AcceptInvitePage(): ReactElement {
  const { t } = useTranslation();
  const { token } = Route.useSearch();

  if (!token) {
    return (
      <PageShell>
        <ErrorState
          icon={<CircleAlert size={40} aria-hidden="true" />}
          message={t('invites.accept.invalid_link')}
        />
      </PageShell>
    );
  }

  return <AcceptInviteForm token={token} />;
}

interface AcceptInviteFormProps {
  token: string;
}

function AcceptInviteForm({ token }: AcceptInviteFormProps): ReactElement {
  const { t } = useTranslation();
  const [result, setResult] = useState<RsvpChoice | null>(null);

  const mutation = useMutation({
    mutationFn: async (rsvp: RsvpChoice): Promise<AcceptInviteResult> => {
      const response = await sdk.POST('/public/invites/accept', {
        body: { token, rsvp },
      });
      if (response.error || !response.data) {
        throw toApiError(response.error, 'Failed to record RSVP');
      }
      return response.data as AcceptInviteResult;
    },
    onSuccess: (_data, rsvp) => {
      setResult(rsvp);
    },
  });

  if (result) {
    const successMessage =
      result === 'accepted'
        ? t('invites.accept.success_accepted')
        : result === 'tentative'
          ? t('invites.accept.success_tentative')
          : t('invites.accept.success_declined');
    return (
      <PageShell>
        <Card>
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              gap: 'var(--nf-space-3)',
              padding: 'var(--nf-space-4) var(--nf-space-2)',
              textAlign: 'center',
            }}
          >
            <CalendarCheck
              size={48}
              aria-hidden="true"
              style={{ color: 'var(--nf-color-accent)' }}
            />
            <h1 style={headingStyle}>{t('invites.accept.success_title')}</h1>
            <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>{successMessage}</p>
          </div>
        </Card>
      </PageShell>
    );
  }

  const error = mutation.error;
  const isNotFound = error instanceof ApiError && error.code === 'CALENDAR.INVITE.NOT_FOUND';

  return (
    <PageShell>
      <Card>
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            gap: 'var(--nf-space-4)',
            padding: 'var(--nf-space-4) var(--nf-space-2)',
            textAlign: 'center',
          }}
        >
          <h1 style={headingStyle}>{t('invites.accept.title')}</h1>
          <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
            {t('invites.accept.prompt')}
          </p>

          {error ? (
            <p
              style={{
                margin: 0,
                padding: 'var(--nf-space-2) var(--nf-space-3)',
                borderRadius: 'var(--nf-radius-sm)',
                backgroundColor: 'var(--nf-color-danger-subtle)',
                color: 'var(--nf-color-danger)',
                fontSize: 'var(--nf-text-sm)',
              }}
              role="alert"
            >
              {isNotFound ? t('invites.accept.not_found') : t('invites.accept.error_generic')}
            </p>
          ) : null}

          <div
            style={{
              display: 'flex',
              flexWrap: 'wrap',
              justifyContent: 'center',
              gap: 'var(--nf-space-2)',
              inlineSize: '100%',
            }}
          >
            <Button
              type="button"
              variant="primary"
              disabled={mutation.isPending}
              onClick={() => mutation.mutate('accepted')}
            >
              {t('invites.accept.action_accept')}
            </Button>
            <Button
              type="button"
              variant="default"
              disabled={mutation.isPending}
              onClick={() => mutation.mutate('tentative')}
            >
              {t('invites.accept.action_tentative')}
            </Button>
            <Button
              type="button"
              variant="ghost"
              disabled={mutation.isPending}
              onClick={() => mutation.mutate('declined')}
            >
              {t('invites.accept.action_decline')}
            </Button>
          </div>

          {mutation.isPending ? (
            <p
              style={{
                margin: 0,
                fontSize: 'var(--nf-text-sm)',
                color: 'var(--nf-color-fg-subtle)',
              }}
            >
              {t('invites.accept.submitting')}
            </p>
          ) : null}
        </div>
      </Card>
    </PageShell>
  );
}

interface ErrorStateProps {
  icon: ReactElement;
  message: string;
}

function ErrorState({ icon, message }: ErrorStateProps): ReactElement {
  return (
    <Card>
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: 'var(--nf-space-3)',
          padding: 'var(--nf-space-6) var(--nf-space-2)',
          textAlign: 'center',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        <span style={{ color: 'var(--nf-color-fg-subtle)' }}>{icon}</span>
        <p style={{ margin: 0 }}>{message}</p>
      </div>
    </Card>
  );
}

function PageShell({ children }: { children: React.ReactNode }): ReactElement {
  return (
    <main
      style={{
        maxWidth: '32rem',
        marginInline: 'auto',
        padding: 'var(--nf-space-6) var(--nf-space-4)',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-4)',
        minBlockSize: '100vh',
        justifyContent: 'center',
      }}
    >
      {children}
    </main>
  );
}

const headingStyle: React.CSSProperties = {
  margin: 0,
  fontFamily: 'var(--font-display)',
  fontSize: 'var(--nf-text-2xl)',
  fontWeight: 'var(--nf-weight-bold)',
  color: 'var(--nf-color-fg)',
};
