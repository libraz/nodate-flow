/**
 * PublicAcceptInvitePage — standalone public page for recording an RSVP
 * against a calendar event invite via a plaintext magic-link token.
 *
 * Used by the `/invites/accept?token=<magic>` route. Unauthenticated: the
 * backend response is intentionally minimal (just inviteId + rsvp), so
 * this page shows a simple confirmation without event enrichment.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import { useMutation } from '@tanstack/react-query';
import { CalendarCheck, CircleAlert } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import PublicPageLayout from '../../components/public-page-layout';
import { ApiError, formatApiError, isNetworkError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

type RsvpChoice = 'accepted' | 'tentative' | 'declined';

interface AcceptInviteResult {
  inviteId: string;
  rsvp: string;
}

export interface PublicAcceptInvitePageProps {
  /** Plaintext magic-link token from the `?token=` query string. */
  token: string | undefined;
}

/**
 * PublicAcceptInvitePage renders either an "invalid link" fallback (when
 * `token` is missing) or the interactive RSVP form.
 */
export default function PublicAcceptInvitePage({
  token,
}: PublicAcceptInvitePageProps): ReactElement {
  const { t } = useTranslation();

  if (!token) {
    return (
      <PublicPageLayout measure="narrow" alignMain="center" mainLabel={t('invites.accept.title')}>
        <ErrorState
          icon={<CircleAlert size={40} aria-hidden="true" />}
          message={t('invites.accept.invalid_link')}
        />
      </PublicPageLayout>
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
      <PublicPageLayout
        measure="narrow"
        alignMain="center"
        mainLabel={t('invites.accept.success_title')}
      >
        <Card>
          <section
            aria-label={t('invites.accept.success_title')}
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
              style={{ color: 'var(--nf-color-accent-fg)' }}
            />
            <h1 style={headingStyle}>{t('invites.accept.success_title')}</h1>
            <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>{successMessage}</p>
          </section>
        </Card>
      </PublicPageLayout>
    );
  }

  const error = mutation.error;
  const isNotFound = error instanceof ApiError && error.code === 'CALENDAR.INVITE.NOT_FOUND';
  const network = isNetworkError(error);

  return (
    <PublicPageLayout measure="narrow" alignMain="center" mainLabel={t('invites.accept.title')}>
      <Card>
        <section
          aria-label={t('invites.accept.title')}
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
                color: 'var(--nf-color-danger-fg)',
                fontSize: 'var(--nf-text-sm)',
              }}
              role="alert"
            >
              {network
                ? t('common.network_error')
                : isNotFound
                  ? t('invites.accept.not_found')
                  : formatApiError(error, t, 'invites.accept.error_generic')}
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
        </section>
      </Card>
    </PublicPageLayout>
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

const headingStyle: React.CSSProperties = {
  margin: 0,
  fontFamily: 'var(--nf-font-display)',
  fontSize: 'var(--nf-text-2xl)',
  fontWeight: 'var(--nf-weight-bold)',
  color: 'var(--nf-color-fg)',
};
