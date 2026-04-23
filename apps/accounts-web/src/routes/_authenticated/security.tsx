/**
 * /security -- Password change, TOTP management, active sessions.
 * Authenticated-only, guarded by the _authenticated layout route.
 */

import { zodResolver } from '@hookform/resolvers/zod';
import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute } from '@tanstack/react-router';
import QRCode from 'qrcode';
import { type FormEvent, type ReactElement, useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';

import AuthCard from '../../components/auth-card';
import {
  type ChangePasswordFormValues,
  changePasswordSchema,
} from '../../features/auth/auth-schemas';
import type { ProblemJson } from '../../lib/api-error';
import {
  type AuthErrorI18nKey,
  extractErrorCode,
  mapAuthError,
  mapAuthThrown,
} from '../../lib/auth-errors';
import { sdk } from '../../lib/sdk';

/** SDK-derived response bodies. Local interfaces caused silent shape drift. */
type SessionSummary = components['schemas']['SessionSummary'];
type ListSessionsOutputBody = components['schemas']['ListSessionsOutputBody'];
type TotpStatusOutputBody = components['schemas']['TotpStatusOutputBody'];
type TotpStatus = TotpStatusOutputBody['status'];
type TotpEnrollOutputBody = components['schemas']['TotpEnrollOutputBody'];
type TotpConfirmOutputBody = components['schemas']['TotpConfirmOutputBody'];
type TotpRegenerateRecoveryCodesOutputBody =
  components['schemas']['TotpRegenerateRecoveryCodesOutputBody'];

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
      const { error } = await sdk.PUT('/me/password', {
        body: {
          currentPassword: values.currentPassword,
          newPassword: values.newPassword,
        },
      });
      if (error) {
        setPasswordError(mapAuthError(error as ProblemJson | undefined));
        return;
      }
      setPasswordSuccess(true);
      reset();
    } catch (err) {
      setPasswordError(mapAuthThrown(err));
    }
  };

  // -- Active sessions --
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [sessionsLoading, setSessionsLoading] = useState(true);
  const [revokingId, setRevokingId] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void sdk
      .GET('/me/sessions')
      .then((res) => {
        if (!cancelled) {
          const body = res.data as ListSessionsOutputBody | undefined;
          setSessions(body?.items ?? []);
          setSessionsLoading(false);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          console.error('Failed to fetch sessions', err);
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
      await sdk.DELETE('/me/sessions/{sessionId}', {
        params: { path: { sessionId } },
      });
      setSessions((prev) => prev.filter((s) => s.id !== sessionId));
    } catch {
      // ignore
    } finally {
      setRevokingId(null);
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
        <h2 style={headingStyle}>{t('security.password_title')}</h2>
        <form
          onSubmit={(e) => {
            void handleSubmit(onChangePassword)(e);
          }}
          noValidate
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4, 1rem)' }}
        >
          <FormField
            label={t('security.current_password')}
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
            label={t('security.new_password')}
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
              {t('security.password_changed')}
            </output>
          ) : null}

          <Button type="submit" variant="primary" disabled={isSubmitting}>
            {isSubmitting ? t('security.password_changing') : t('security.password_change')}
          </Button>
        </form>
      </section>

      <hr style={dividerStyle} />

      {/* TOTP management */}
      <section style={sectionStyle}>
        <h2 style={headingStyle}>{t('security.totp.title')}</h2>
        <TotpSection />
      </section>

      <hr style={dividerStyle} />

      {/* Active sessions */}
      <section style={sectionStyle}>
        <h2 style={headingStyle}>{t('security.sessions_title')}</h2>
        {sessionsLoading ? (
          <p
            style={{
              margin: 0,
              fontSize: 'var(--nf-text-sm, 0.875rem)',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('security.sessions_loading')}
          </p>
        ) : sessions.length === 0 ? (
          <p
            style={{
              margin: 0,
              fontSize: 'var(--nf-text-sm, 0.875rem)',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('security.sessions_empty')}
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
                    {session.userAgent || t('security.session_unknown_agent')}
                    {session.current ? ` (${t('security.session_current')})` : ''}
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
                    {t('security.session_revoke')}
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
        <Link to="/profile">{t('security.profile_link')}</Link>
      </p>
    </AuthCard>
  );
}

/**
 * TotpSection — three-step wizard driven by the server-side
 * `{status: "disabled" | "pending" | "enabled"}` state.
 */
function TotpSection(): ReactElement {
  const { t } = useTranslation('auth');
  const [status, setStatus] = useState<TotpStatus | null>(null);
  const [enrollment, setEnrollment] = useState<TotpEnrollOutputBody | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [errorKey, setErrorKey] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Load initial status. The server only returns `status`, not the
  // otpauth URL / secret, so a page reload mid-enrollment drops us
  // back to a "resume" affordance (see pending branch below).
  useEffect(() => {
    let cancelled = false;
    void sdk
      .GET('/me/totp')
      .then((res) => {
        if (!cancelled && res.data) {
          const body = res.data as TotpStatusOutputBody;
          setStatus(body.status);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          console.error('Failed to fetch TOTP status', err);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const startEnroll = async (): Promise<void> => {
    setErrorKey(null);
    setBusy(true);
    try {
      const { data, error } = await sdk.POST('/me/totp/enroll');
      if (error) {
        setErrorKey('security.totp.errors.enroll_failed');
        return;
      }
      const body = data as TotpEnrollOutputBody;
      setEnrollment(body);
      setStatus('pending');
    } catch {
      setErrorKey('security.totp.errors.enroll_failed');
    } finally {
      setBusy(false);
    }
  };

  const cancelEnroll = async (): Promise<void> => {
    setErrorKey(null);
    setBusy(true);
    try {
      await sdk.DELETE('/me/totp', { body: { password: '' } });
    } catch {
      // ignore — best-effort cancel
    } finally {
      setEnrollment(null);
      setStatus('disabled');
      setBusy(false);
    }
  };

  const confirmEnroll = async (code: string): Promise<void> => {
    setErrorKey(null);
    setBusy(true);
    try {
      const { data, error } = await sdk.POST('/me/totp/confirm', { body: { code } });
      if (error) {
        const errCode = extractErrorCode(error as ProblemJson | undefined);
        if (errCode === 'AUTH.TOTP.CODE_MISMATCH') {
          setErrorKey('security.totp.errors.code_mismatch');
        } else {
          setErrorKey('security.totp.errors.confirm_failed');
        }
        return;
      }
      const body = data as TotpConfirmOutputBody;
      setRecoveryCodes(body.recoveryCodes ?? []);
      setEnrollment(null);
      setStatus('enabled');
    } catch {
      setErrorKey('security.totp.errors.confirm_failed');
    } finally {
      setBusy(false);
    }
  };

  if (status === null) {
    return (
      <p
        style={{
          margin: 0,
          fontSize: 'var(--nf-text-sm, 0.875rem)',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {t('security.totp.loading')}
      </p>
    );
  }

  // After confirm, show recovery codes once.
  if (recoveryCodes !== null) {
    return (
      <RecoveryCodesView
        codes={recoveryCodes}
        onDismiss={() => {
          setRecoveryCodes(null);
        }}
      />
    );
  }

  if (status === 'disabled') {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3, 0.75rem)' }}>
        <p
          style={{
            margin: 0,
            fontSize: 'var(--nf-text-sm, 0.875rem)',
            color: 'var(--nf-color-fg-muted)',
          }}
        >
          {t('security.totp.disabled_description')}
        </p>
        {errorKey ? (
          <p
            role="alert"
            style={{
              margin: 0,
              color: 'var(--nf-color-danger)',
              fontSize: 'var(--nf-text-sm, 0.875rem)',
            }}
          >
            {t(errorKey)}
          </p>
        ) : null}
        <div>
          <Button
            type="button"
            variant="primary"
            disabled={busy}
            onClick={() => {
              void startEnroll();
            }}
          >
            {t('security.totp.start_setup')}
          </Button>
        </div>
      </div>
    );
  }

  if (status === 'pending') {
    if (enrollment === null) {
      // Page reloaded mid-enrollment: the server never returns the
      // otpauth URL / secret on GET /me/totp, so we cannot resume
      // the QR step. Offer to restart (POST /me/totp/enroll is
      // idempotent in the pending state and rotates the secret).
      return (
        <div
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3, 0.75rem)' }}
        >
          <p
            style={{
              margin: 0,
              fontSize: 'var(--nf-text-sm, 0.875rem)',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('security.totp.pending_description')}
          </p>
          {errorKey ? (
            <p
              role="alert"
              style={{
                margin: 0,
                color: 'var(--nf-color-danger)',
                fontSize: 'var(--nf-text-sm, 0.875rem)',
              }}
            >
              {t(errorKey)}
            </p>
          ) : null}
          <div style={{ display: 'flex', gap: 'var(--nf-space-2, 0.5rem)', flexWrap: 'wrap' }}>
            <Button
              type="button"
              variant="primary"
              disabled={busy}
              onClick={() => {
                void startEnroll();
              }}
            >
              {t('security.totp.resume_restart')}
            </Button>
            <Button
              type="button"
              variant="ghost"
              disabled={busy}
              onClick={() => {
                void cancelEnroll();
              }}
            >
              {t('security.totp.cancel')}
            </Button>
          </div>
        </div>
      );
    }
    return (
      <EnrollmentStep
        enrollment={enrollment}
        busy={busy}
        errorKey={errorKey}
        onConfirm={(code) => {
          void confirmEnroll(code);
        }}
        onCancel={() => {
          void cancelEnroll();
        }}
      />
    );
  }

  // status === 'enabled'
  return (
    <EnabledPanel
      onRegenerated={(codes) => {
        setRecoveryCodes(codes);
      }}
      onDisabled={() => {
        setStatus('disabled');
      }}
    />
  );
}

/**
 * EnrollmentStep — renders the QR code, secret, and code-confirm form.
 */
function EnrollmentStep({
  enrollment,
  busy,
  errorKey,
  onConfirm,
  onCancel,
}: {
  enrollment: TotpEnrollOutputBody;
  busy: boolean;
  errorKey: string | null;
  onConfirm: (code: string) => void;
  onCancel: () => void;
}): ReactElement {
  const { t } = useTranslation('auth');
  const [code, setCode] = useState('');
  const [qrDataUrl, setQrDataUrl] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    QRCode.toDataURL(enrollment.otpauthUrl, { width: 200, margin: 1 })
      .then((url: string) => {
        if (!cancelled) setQrDataUrl(url);
      })
      .catch(() => {
        if (!cancelled) setQrDataUrl(null);
      });
    return () => {
      cancelled = true;
    };
  }, [enrollment.otpauthUrl]);

  const handleSubmit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    if (code.length === 6) onConfirm(code);
  };

  return (
    <form
      onSubmit={handleSubmit}
      style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3, 0.75rem)' }}
    >
      <p style={{ margin: 0, fontSize: 'var(--nf-text-sm, 0.875rem)' }}>
        {t('security.totp.enroll_instructions')}
      </p>
      {qrDataUrl ? (
        <img
          src={qrDataUrl}
          alt={t('security.totp.qr_label')}
          width={200}
          height={200}
          style={{ alignSelf: 'center' }}
        />
      ) : null}
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'auto 1fr',
          gap: 'var(--nf-space-2, 0.5rem)',
          alignItems: 'center',
          padding: 'var(--nf-space-3, 0.75rem)',
          border: 'var(--nf-space-px, 1px) solid var(--nf-color-border)',
          borderRadius: 'var(--nf-radius-md, 0.375rem)',
        }}
      >
        <span style={{ fontWeight: 500, fontSize: 'var(--nf-text-sm, 0.875rem)' }}>
          {t('security.totp.manual_secret_label')}
        </span>
        <code
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 'var(--nf-text-sm, 0.875rem)',
            wordBreak: 'break-all',
            userSelect: 'all',
          }}
        >
          {enrollment.secret}
        </code>
      </div>
      <FormField label={t('security.totp.code_input_label')} required>
        {(control) => (
          <Input
            {...control}
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9]{6}"
            maxLength={6}
            value={code}
            onChange={(e) => {
              setCode(e.target.value.replace(/\D/g, '').slice(0, 6));
            }}
            required
          />
        )}
      </FormField>
      {errorKey ? (
        <p
          role="alert"
          style={{
            margin: 0,
            color: 'var(--nf-color-danger)',
            fontSize: 'var(--nf-text-sm, 0.875rem)',
          }}
        >
          {t(errorKey)}
        </p>
      ) : null}
      <div style={{ display: 'flex', gap: 'var(--nf-space-2, 0.5rem)', flexWrap: 'wrap' }}>
        <Button type="submit" variant="primary" disabled={busy || code.length !== 6}>
          {busy ? t('security.totp.confirming') : t('security.totp.confirm')}
        </Button>
        <Button type="button" variant="ghost" disabled={busy} onClick={onCancel}>
          {t('security.totp.cancel')}
        </Button>
      </div>
    </form>
  );
}

/**
 * RecoveryCodesView — shown once after confirm or regenerate. The codes
 * are never returned again, so the user must save them now.
 */
function RecoveryCodesView({
  codes,
  onDismiss,
}: {
  codes: string[];
  onDismiss: () => void;
}): ReactElement {
  const { t } = useTranslation('auth');
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3, 0.75rem)' }}>
      <h3 style={{ margin: 0, fontSize: 'var(--nf-text-base, 1rem)' }}>
        {t('security.totp.recovery_codes_title')}
      </h3>
      <p
        role="alert"
        style={{
          margin: 0,
          color: 'var(--nf-color-warning, var(--nf-color-danger))',
          fontSize: 'var(--nf-text-sm, 0.875rem)',
          fontWeight: 600,
        }}
      >
        {t('security.totp.recovery_codes_warning')}
      </p>
      <ul
        style={{
          listStyle: 'none',
          padding: 'var(--nf-space-3, 0.75rem)',
          margin: 0,
          border: 'var(--nf-space-px, 1px) solid var(--nf-color-border)',
          borderRadius: 'var(--nf-radius-md, 0.375rem)',
          fontFamily: 'var(--font-mono)',
          fontSize: 'var(--nf-text-sm, 0.875rem)',
          display: 'grid',
          gridTemplateColumns: '1fr 1fr',
          gap: 'var(--nf-space-1, 0.25rem) var(--nf-space-4, 1rem)',
        }}
      >
        {codes.map((c) => (
          <li key={c} style={{ userSelect: 'all' }}>
            {c}
          </li>
        ))}
      </ul>
      <div>
        <Button type="button" variant="primary" onClick={onDismiss}>
          {t('security.totp.recovery_codes_done')}
        </Button>
      </div>
    </div>
  );
}

/**
 * EnabledPanel — when TOTP is enabled, offers disable (password-gated)
 * and regenerate-recovery-codes (password-gated).
 */
function EnabledPanel({
  onRegenerated,
  onDisabled,
}: {
  onRegenerated: (codes: string[]) => void;
  onDisabled: () => void;
}): ReactElement {
  const { t } = useTranslation('auth');
  const [mode, setMode] = useState<'idle' | 'disable' | 'regenerate'>('idle');
  const [password, setPassword] = useState('');
  const [errorKey, setErrorKey] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const resetForm = (): void => {
    setPassword('');
    setErrorKey(null);
    setMode('idle');
  };

  const handleDisable = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    setErrorKey(null);
    setBusy(true);
    try {
      const { error } = await sdk.DELETE('/me/totp', { body: { password } });
      if (error) {
        const errCode = extractErrorCode(error as ProblemJson | undefined);
        if (errCode === 'AUTH.PASSWORD.CURRENT_MISMATCH') {
          setErrorKey('security.totp.errors.password_mismatch');
        } else {
          setErrorKey('security.totp.errors.disable_failed');
        }
        return;
      }
      resetForm();
      onDisabled();
    } catch {
      setErrorKey('security.totp.errors.disable_failed');
    } finally {
      setBusy(false);
    }
  };

  const handleRegenerate = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    setErrorKey(null);
    setBusy(true);
    try {
      const { data, error } = await sdk.POST('/me/totp/recovery-codes', { body: { password } });
      if (error) {
        const errCode = extractErrorCode(error as ProblemJson | undefined);
        if (errCode === 'AUTH.PASSWORD.CURRENT_MISMATCH') {
          setErrorKey('security.totp.errors.password_mismatch');
        } else {
          setErrorKey('security.totp.errors.regenerate_failed');
        }
        return;
      }
      const body = data as TotpRegenerateRecoveryCodesOutputBody;
      resetForm();
      onRegenerated(body.recoveryCodes ?? []);
    } catch {
      setErrorKey('security.totp.errors.regenerate_failed');
    } finally {
      setBusy(false);
    }
  };

  if (mode === 'idle') {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3, 0.75rem)' }}>
        <p
          style={{
            margin: 0,
            fontSize: 'var(--nf-text-sm, 0.875rem)',
            color: 'var(--nf-color-fg-muted)',
          }}
        >
          {t('security.totp.enabled_description')}
        </p>
        <div style={{ display: 'flex', gap: 'var(--nf-space-2, 0.5rem)', flexWrap: 'wrap' }}>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setMode('disable');
            }}
          >
            {t('security.totp.disable')}
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setMode('regenerate');
            }}
          >
            {t('security.totp.regenerate')}
          </Button>
        </div>
      </div>
    );
  }

  const title =
    mode === 'disable' ? t('security.totp.disable_confirm') : t('security.totp.regenerate_confirm');
  const submitLabel =
    mode === 'disable' ? t('security.totp.disable') : t('security.totp.regenerate');

  return (
    <form
      onSubmit={(e) => {
        if (mode === 'disable') void handleDisable(e);
        else void handleRegenerate(e);
      }}
      style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3, 0.75rem)' }}
    >
      <p style={{ margin: 0, fontSize: 'var(--nf-text-sm, 0.875rem)' }}>{title}</p>
      <FormField label={t('security.totp.password_required')} required>
        {(control) => (
          <Input
            {...control}
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => {
              setPassword(e.target.value);
            }}
            required
          />
        )}
      </FormField>
      {errorKey ? (
        <p
          role="alert"
          style={{
            margin: 0,
            color: 'var(--nf-color-danger)',
            fontSize: 'var(--nf-text-sm, 0.875rem)',
          }}
        >
          {t(errorKey)}
        </p>
      ) : null}
      <div style={{ display: 'flex', gap: 'var(--nf-space-2, 0.5rem)', flexWrap: 'wrap' }}>
        <Button
          type="submit"
          variant={mode === 'disable' ? 'danger' : 'primary'}
          disabled={busy || password === ''}
        >
          {submitLabel}
        </Button>
        <Button type="button" variant="ghost" disabled={busy} onClick={resetForm}>
          {t('security.totp.cancel')}
        </Button>
      </div>
    </form>
  );
}

export const Route = createFileRoute('/_authenticated/security')({
  component: SecurityPage,
});
