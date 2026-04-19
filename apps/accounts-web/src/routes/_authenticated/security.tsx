/**
 * /security -- Password change, TOTP management, active sessions.
 * Authenticated-only, guarded by the _authenticated layout route.
 */

import { zodResolver } from '@hookform/resolvers/zod';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';

import AuthCard from '../../components/auth-card';
import { apiRequest } from '../../lib/api-client';
import { type AuthErrorI18nKey, mapAuthError, mapAuthThrown } from '../../lib/auth-errors';
import { type ChangePasswordFormValues, changePasswordSchema } from '../../lib/auth-schemas';

interface Session {
  id: string;
  userAgent: string;
  ipAddress: string;
  createdAt: number;
  current: boolean;
}

interface SessionsResponse {
  sessions: Session[];
}

interface TotpStatusResponse {
  enrolled: boolean;
}

function SecurityPage(): ReactElement {
  const { t } = useTranslation('auth');

  // -- Password change --
  const [passwordError, setPasswordError] = useState<AuthErrorI18nKey | null>(null);
  const [passwordSuccess, setPasswordSuccess] = useState(false);

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<ChangePasswordFormValues>({
    resolver: zodResolver(changePasswordSchema),
    defaultValues: { currentPassword: '', newPassword: '' },
  });

  const onChangePassword = async (values: ChangePasswordFormValues): Promise<void> => {
    setPasswordError(null);
    setPasswordSuccess(false);
    try {
      const result = await apiRequest<null>('/auth/password', {
        method: 'PUT',
        body: {
          currentPassword: values.currentPassword,
          newPassword: values.newPassword,
        },
      });
      if (result.error) {
        setPasswordError(mapAuthError(result.error));
        return;
      }
      setPasswordSuccess(true);
      reset();
    } catch (err) {
      setPasswordError(mapAuthThrown(err));
    }
  };

  // -- TOTP management --
  const [totpEnrolled, setTotpEnrolled] = useState<boolean | null>(null);
  const [totpLoading, setTotpLoading] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void apiRequest<TotpStatusResponse>('/auth/totp/status').then((res) => {
      if (!cancelled && res.data) {
        setTotpEnrolled(res.data.enrolled);
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  // -- Active sessions --
  const [sessions, setSessions] = useState<Session[]>([]);
  const [sessionsLoading, setSessionsLoading] = useState(true);
  const [revokingId, setRevokingId] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void apiRequest<SessionsResponse>('/auth/sessions').then((res) => {
      if (!cancelled) {
        setSessions(res.data?.sessions ?? []);
        setSessionsLoading(false);
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const handleRevokeSession = async (sessionId: string): Promise<void> => {
    setRevokingId(sessionId);
    try {
      await apiRequest<null>(`/auth/sessions/${sessionId}`, { method: 'DELETE' });
      setSessions((prev) => prev.filter((s) => s.id !== sessionId));
    } catch {
      // ignore
    } finally {
      setRevokingId(null);
    }
  };

  const handleToggleTotp = async (): Promise<void> => {
    setTotpLoading(true);
    try {
      if (totpEnrolled) {
        const result = await apiRequest<null>('/auth/totp', { method: 'DELETE' });
        if (!result.error) {
          setTotpEnrolled(false);
        }
      } else {
        const result = await apiRequest<null>('/auth/totp', { method: 'POST' });
        if (!result.error) {
          setTotpEnrolled(true);
        }
      }
    } catch {
      // ignore
    } finally {
      setTotpLoading(false);
    }
  };

  const sectionStyle = {
    display: 'flex',
    flexDirection: 'column' as const,
    gap: 'var(--nf-space-4, 1rem)',
  };

  const headingStyle = {
    fontFamily: 'var(--nf-font-display, var(--font-display))',
    fontSize: 'var(--nf-text-lg, 1.125rem)',
    margin: 0,
  };

  const dividerStyle = {
    border: 'none',
    borderBlockStart: 'var(--nf-space-px, 1px) solid var(--nf-color-border)',
    margin: 0,
  };

  return (
    <AuthCard>
      <h1
        style={{
          fontFamily: 'var(--nf-font-display, var(--font-display))',
          fontSize: 'var(--nf-text-2xl, 1.5rem)',
          margin: 0,
        }}
      >
        {t('security.title')}
      </h1>

      {/* Password change */}
      <section style={sectionStyle}>
        <h2 style={headingStyle}>{t('security.passwordTitle')}</h2>
        <form
          onSubmit={(e) => {
            void handleSubmit(onChangePassword)(e);
          }}
          noValidate
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4, 1rem)' }}
        >
          <FormField
            label={t('security.currentPassword')}
            required
            {...(errors.currentPassword?.message
              ? { error: t(errors.currentPassword.message) }
              : {})}
          >
            {(control) => {
              const { ref, ...field } = register('currentPassword');
              return (
                <Input
                  {...control}
                  {...field}
                  ref={ref}
                  type="password"
                  autoComplete="current-password"
                />
              );
            }}
          </FormField>

          <FormField
            label={t('security.newPassword')}
            required
            {...(errors.newPassword?.message ? { error: t(errors.newPassword.message) } : {})}
          >
            {(control) => {
              const { ref, ...field } = register('newPassword');
              return (
                <Input
                  {...control}
                  {...field}
                  ref={ref}
                  type="password"
                  autoComplete="new-password"
                />
              );
            }}
          </FormField>

          {passwordError ? (
            <p
              role="alert"
              style={{
                margin: 0,
                color: 'var(--nf-color-danger)',
                fontSize: 'var(--nf-text-sm, 0.875rem)',
              }}
            >
              {t(passwordError)}
            </p>
          ) : null}

          {passwordSuccess ? (
            <output
              style={{
                margin: 0,
                color: 'var(--nf-color-success, var(--nf-color-success))',
                fontSize: 'var(--nf-text-sm, 0.875rem)',
              }}
            >
              {t('security.passwordChanged')}
            </output>
          ) : null}

          <Button type="submit" variant="primary" disabled={isSubmitting}>
            {isSubmitting ? t('security.passwordChanging') : t('security.passwordChange')}
          </Button>
        </form>
      </section>

      <hr style={dividerStyle} />

      {/* TOTP management */}
      <section style={sectionStyle}>
        <h2 style={headingStyle}>{t('security.totpTitle')}</h2>
        <p
          style={{
            margin: 0,
            fontSize: 'var(--nf-text-sm, 0.875rem)',
            color: 'var(--nf-color-fg-muted)',
          }}
        >
          {totpEnrolled === null
            ? t('security.totpLoading')
            : totpEnrolled
              ? t('security.totpEnabled')
              : t('security.totpDisabled')}
        </p>
        {totpEnrolled !== null ? (
          <Button
            type="button"
            variant={totpEnrolled ? 'ghost' : 'primary'}
            disabled={totpLoading}
            onClick={() => {
              void handleToggleTotp();
            }}
          >
            {totpEnrolled ? t('security.totpDisable') : t('security.totpEnable')}
          </Button>
        ) : null}
      </section>

      <hr style={dividerStyle} />

      {/* Active sessions */}
      <section style={sectionStyle}>
        <h2 style={headingStyle}>{t('security.sessionsTitle')}</h2>
        {sessionsLoading ? (
          <p
            style={{
              margin: 0,
              fontSize: 'var(--nf-text-sm, 0.875rem)',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('security.sessionsLoading')}
          </p>
        ) : sessions.length === 0 ? (
          <p
            style={{
              margin: 0,
              fontSize: 'var(--nf-text-sm, 0.875rem)',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('security.sessionsEmpty')}
          </p>
        ) : (
          <ul
            style={{
              listStyle: 'none',
              margin: 0,
              padding: 0,
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--nf-space-3, 0.75rem)',
            }}
          >
            {sessions.map((session) => (
              <li
                key={session.id}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  padding: 'var(--nf-space-3, 0.75rem)',
                  borderRadius: 'var(--nf-radius-md, 0.375rem)',
                  border: 'var(--nf-space-px, 1px) solid var(--nf-color-border)',
                  background: session.current ? 'var(--nf-color-bg-elevated)' : 'transparent',
                }}
              >
                <div>
                  <p
                    style={{
                      margin: 0,
                      fontSize: 'var(--nf-text-sm, 0.875rem)',
                      fontWeight: session.current ? 600 : 400,
                    }}
                  >
                    {session.userAgent || t('security.sessionUnknownAgent')}
                    {session.current ? ` (${t('security.sessionCurrent')})` : ''}
                  </p>
                  <p
                    style={{
                      margin: 0,
                      fontSize: 'var(--nf-text-xs, 0.75rem)',
                      color: 'var(--nf-color-fg-muted)',
                    }}
                  >
                    {session.ipAddress}
                  </p>
                </div>
                {!session.current ? (
                  <Button
                    type="button"
                    variant="ghost"
                    disabled={revokingId === session.id}
                    onClick={() => {
                      void handleRevokeSession(session.id);
                    }}
                  >
                    {t('security.sessionRevoke')}
                  </Button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>

      <hr style={dividerStyle} />

      <p
        style={{
          margin: 0,
          fontSize: 'var(--nf-text-sm, 0.875rem)',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        <Link to="/profile">{t('security.profileLink')}</Link>
      </p>
    </AuthCard>
  );
}

export const Route = createFileRoute('/_authenticated/security')({
  component: SecurityPage,
});
