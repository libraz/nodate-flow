/**
 * /security -- Password change, TOTP management, active sessions.
 * Authenticated-only, guarded by the _authenticated layout route.
 */

import type { components } from '@nodate-flow/sdk';
import { useZodForm } from '@nodate-flow/ui/hooks/use-zod-form';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { createFileRoute, Link } from '@tanstack/react-router';
import QRCode from 'qrcode';
import { type FormEvent, type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AuthCard from '../../components/auth-card';
import {
  type ChangePasswordFormValues,
  changePasswordSchema,
} from '../../features/auth/auth-schemas';
import PasswordInput from '../../features/auth/password-input';
import { useCapsLockHint } from '../../features/auth/use-caps-lock-hint';
import type { ProblemJson } from '../../lib/api-error';
import {
  type AuthErrorI18nKey,
  extractErrorCode,
  mapAuthError,
  mapAuthThrown,
} from '../../lib/auth-errors';
import { logError } from '../../lib/log';
import { sdk } from '../../lib/sdk';

import recoveryStyles from './recovery-codes.module.css';

/** SDK-derived response bodies. Local interfaces caused silent shape drift. */
type SessionSummary = components['schemas']['SessionSummary'];
type ListSessionsOutputBody = components['schemas']['ListSessionsOutputBody'];
type TotpStatusOutputBody = components['schemas']['TotpStatusOutputBody'];
type TotpStatus = TotpStatusOutputBody['status'];
type TotpEnrollOutputBody = components['schemas']['TotpEnrollOutputBody'];
type TotpConfirmOutputBody = components['schemas']['TotpConfirmOutputBody'];
type TotpRegenerateRecoveryCodesOutputBody =
  components['schemas']['TotpRegenerateRecoveryCodesOutputBody'];

/**
 * @brief Identifier returned by parseUserAgent when a token is recognized.
 * Concrete browser/OS labels remain English-derived strings (e.g. "Chrome 124"),
 * while the unknown tokens are translation keys resolved at render time so
 * non-English locales don't see a hardcoded "Unknown".
 */
type BrowserToken = { kind: 'name'; label: string } | { kind: 'unknown' };
type OsToken = { kind: 'name'; label: string } | { kind: 'unknown' };

interface ParsedUA {
  browser: BrowserToken;
  os: OsToken;
}

/**
 * @brief Extract the numeric version token that follows a product marker.
 * @param ua User-Agent string.
 * @param marker Product marker including trailing slash (e.g. "Chrome/").
 * @return Numeric major version as a string, or empty string when not found.
 *
 * Avoids regex per project convention: locates the marker via indexOf, then
 * consumes leading digits until a non-digit byte.
 */
export function extractVersion(ua: string, marker: string): string {
  const idx = ua.indexOf(marker);
  if (idx === -1) return '';
  let i = idx + marker.length;
  let version = '';
  while (i < ua.length) {
    const ch = ua.charCodeAt(i);
    if (ch < 48 || ch > 57) break; // not 0-9
    version += ua[i];
    i += 1;
  }
  return version;
}

/**
 * @brief Parse a User-Agent string into glanceable browser + OS tokens.
 * @param ua Raw User-Agent header value.
 * @return Tagged tokens; `kind === 'unknown'` lets the caller pick the
 *         appropriate i18n key instead of receiving a hardcoded "Unknown".
 *
 * Hand-rolled, regex-free parser covering common desktop and mobile agents.
 * Order matters for browsers: Edge UA embeds "Chrome/" and Chrome UA embeds
 * "Safari/", so detection runs Edge -> Firefox -> Chrome -> Safari.
 */
export function parseUserAgent(ua: string): ParsedUA {
  let browser: BrowserToken = { kind: 'unknown' };
  const edgeVer = extractVersion(ua, 'Edg/');
  const firefoxVer = extractVersion(ua, 'Firefox/');
  const chromeVer = extractVersion(ua, 'Chrome/');
  const safariVer = extractVersion(ua, 'Version/');
  if (edgeVer) browser = { kind: 'name', label: `Edge ${edgeVer}` };
  else if (firefoxVer) browser = { kind: 'name', label: `Firefox ${firefoxVer}` };
  else if (chromeVer) browser = { kind: 'name', label: `Chrome ${chromeVer}` };
  else if (safariVer && ua.indexOf('Safari/') !== -1)
    browser = { kind: 'name', label: `Safari ${safariVer}` };

  // Order matters: iPad/iPhone UAs include "Mac OS X" / "Macintosh", and
  // Android UAs include "Linux", so the more specific match must come first.
  let os: OsToken = { kind: 'unknown' };
  if (ua.indexOf('Windows NT') !== -1) os = { kind: 'name', label: 'Windows' };
  else if (ua.indexOf('Android') !== -1) os = { kind: 'name', label: 'Android' };
  else if (ua.indexOf('iPhone') !== -1 || ua.indexOf('iPad') !== -1 || ua.indexOf('iOS') !== -1)
    os = { kind: 'name', label: 'iOS' };
  else if (ua.indexOf('Mac OS X') !== -1 || ua.indexOf('Macintosh') !== -1)
    os = { kind: 'name', label: 'macOS' };
  else if (ua.indexOf('Linux') !== -1) os = { kind: 'name', label: 'Linux' };

  return { browser, os };
}

/** Seconds in a day — shared between relative and absolute branches. */
const SECONDS_PER_DAY = 86_400;
/** Window within which we prefer a relative (e.g. "2 hours ago") label. */
const RELATIVE_WINDOW_SECONDS = 7 * SECONDS_PER_DAY;

/**
 * @brief Render a unix-seconds timestamp as a locale-aware string.
 * @param ts Unix seconds.
 * @param locale BCP 47 locale (e.g. "ja" / "en").
 * @return Relative phrase (e.g. "2 時間前") within 7 days, otherwise a
 *         locale-formatted absolute date.
 *
 * Intentionally local to this file: the audit-logs page renders absolute
 * timestamps only, so the two use cases do not share a util yet. Uses
 * `Intl.RelativeTimeFormat` and `Intl.DateTimeFormat` — no date library.
 */
function formatRelative(ts: number, locale: string): string {
  const nowSec = Math.floor(Date.now() / 1000);
  const diffSec = ts - nowSec; // negative = past, positive = future
  const absDiff = Math.abs(diffSec);

  if (absDiff < RELATIVE_WINDOW_SECONDS) {
    try {
      const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
      if (absDiff < 60) {
        return rtf.format(Math.round(diffSec), 'second');
      }
      if (absDiff < 3_600) {
        return rtf.format(Math.round(diffSec / 60), 'minute');
      }
      if (absDiff < SECONDS_PER_DAY) {
        return rtf.format(Math.round(diffSec / 3_600), 'hour');
      }
      return rtf.format(Math.round(diffSec / SECONDS_PER_DAY), 'day');
    } catch {
      // Fall through to absolute format on environments lacking RTF.
    }
  }

  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(ts * 1000));
  } catch {
    return new Date(ts * 1000).toISOString();
  }
}

export function SecurityPage(): ReactElement {
  const { t, i18n } = useTranslation('auth');
  const locale = i18n.resolvedLanguage ?? 'en';

  // -- Password change --
  const [passwordError, setPasswordError] = useState<AuthErrorI18nKey | null>(null);
  const [passwordSuccess, setPasswordSuccess] = useState(false);

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useZodForm<ChangePasswordFormValues>(changePasswordSchema, {
    currentPassword: '',
    newPassword: '',
  });

  const onChangePassword = async (values: ChangePasswordFormValues): Promise<void> => {
    setPasswordError(null);
    setPasswordSuccess(false);
    try {
      // IMPORTANT: backend registers POST /me/password. Using PUT returns 405
      // and openapi-fetch does not populate `error` for unmapped status codes,
      // which previously caused the UI to display a false-success toast even
      // though the password was not rotated. Always guard on response.ok as
      // defense-in-depth for any non-2xx that escapes typed-error mapping.
      const { error, response } = await sdk.POST('/me/password', {
        body: {
          currentPassword: values.currentPassword,
          newPassword: values.newPassword,
        },
      });
      if (error) {
        setPasswordError(mapAuthError(error as ProblemJson | undefined));
        return;
      }
      if (!response.ok) {
        setPasswordError(mapAuthError(undefined));
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
          logError('Failed to fetch sessions', err);
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
      const { error } = await sdk.DELETE('/me/sessions/{sessionId}', {
        params: { path: { sessionId } },
      });
      if (error) {
        toaster.show({ message: t('security.session_revoke_failed'), tone: 'danger' });
        return;
      }
      setSessions((prev) => prev.filter((s) => s.id !== sessionId));
    } catch {
      toaster.show({ message: t('security.session_revoke_failed'), tone: 'danger' });
    } finally {
      setRevokingId(null);
    }
  };

  /**
   * Build the localized "Browser · OS" label without leaking English
   * "Unknown" into JA/ZH locales.
   */
  const formatDevice = (ua: string): string => {
    const { browser, os } = parseUserAgent(ua);
    const browserLabel =
      browser.kind === 'name' ? browser.label : t('security.session.browser_unknown');
    const osLabel = os.kind === 'name' ? os.label : t('security.session.os_unknown');
    return `${browserLabel} · ${osLabel}`;
  };

  return (
    <AuthCard>
      <h1 className="aw-page-title">{t('security.title')}</h1>

      {/* Password change */}
      <section className="aw-stack aw-stack-4">
        <h2 className="aw-section-title">{t('security.password_title')}</h2>
        <form
          onSubmit={(e) => {
            void handleSubmit(onChangePassword)(e);
          }}
          noValidate
          className="aw-stack aw-stack-4"
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
                <PasswordInput {...control} {...field} ref={ref} autoComplete="current-password" />
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
                <PasswordInput {...control} {...field} ref={ref} autoComplete="new-password" />
              );
            }}
          </FormField>

          {passwordError ? (
            <p role="alert" className="aw-error">
              {t(passwordError)}
            </p>
          ) : null}

          {passwordSuccess ? (
            <output className="aw-success">{t('security.password_changed')}</output>
          ) : null}

          <Button type="submit" variant="primary" disabled={isSubmitting}>
            {isSubmitting ? t('security.password_changing') : t('security.password_change')}
          </Button>
        </form>
      </section>

      <hr className="aw-rule" />

      {/* TOTP management */}
      <section className="aw-stack aw-stack-4">
        <h2 className="aw-section-title">{t('security.totp.title')}</h2>
        <TotpSection />
      </section>

      <hr className="aw-rule" />

      {/* Active sessions */}
      <section className="aw-stack aw-stack-4">
        <h2 className="aw-section-title">{t('security.sessions_title')}</h2>
        {sessionsLoading ? (
          <p className="aw-flush aw-muted aw-text-sm">{t('security.sessions_loading')}</p>
        ) : sessions.length === 0 ? (
          <p className="aw-flush aw-muted aw-text-sm">{t('security.sessions_empty')}</p>
        ) : (
          <ul className="aw-list">
            {sessions.map((session) => (
              <li
                key={session.id}
                className={`aw-row-between aw-surface ${session.current ? 'aw-surface-elevated' : ''}`}
              >
                <div>
                  <p
                    className={`aw-flush aw-text-sm ${session.current ? 'aw-weight-semibold' : ''}`}
                    title={session.userAgent || undefined}
                  >
                    {session.userAgent
                      ? formatDevice(session.userAgent)
                      : t('security.session_unknown_agent')}
                    {session.current ? ` (${t('security.session_current')})` : ''}
                  </p>
                  <p className="aw-flush aw-muted aw-text-xs">{session.ipAddress}</p>
                  <p className="aw-flush aw-muted aw-text-xs">
                    {t('security.session_created')}: {formatRelative(session.createdAt, locale)}
                    {' · '}
                    {t('security.session_last_active')}:{' '}
                    {session.lastUsedAt !== undefined && session.lastUsedAt !== null
                      ? formatRelative(session.lastUsedAt, locale)
                      : t('security.session_last_active_unknown')}
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

      <hr className="aw-rule" />

      <p className="aw-flush aw-muted aw-text-sm">
        <Link to="/profile" className="aw-link">
          {t('security.profile_link')}
        </Link>
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
          logError('Failed to fetch TOTP status', err);
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
    return <p className="aw-flush aw-muted aw-text-sm">{t('security.totp.loading')}</p>;
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
      <div className="aw-stack aw-stack-3">
        <p className="aw-flush aw-muted aw-text-sm">{t('security.totp.disabled_description')}</p>
        {errorKey ? (
          <p role="alert" className="aw-error">
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
        <div className="aw-stack aw-stack-3">
          <p className="aw-flush aw-muted aw-text-sm">{t('security.totp.pending_description')}</p>
          {errorKey ? (
            <p role="alert" className="aw-error">
              {t(errorKey)}
            </p>
          ) : null}
          <div className="aw-actions">
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
 *
 * If QR generation fails (e.g. canvas blocked, library load error), the
 * fallback surface promotes the secret string for manual entry so the user
 * is never left looking at a blank panel mid-enrollment.
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
  const [qrFailed, setQrFailed] = useState(false);
  const capsHint = useCapsLockHint();

  useEffect(() => {
    let cancelled = false;
    setQrFailed(false);
    QRCode.toDataURL(enrollment.otpauthUrl, { width: 200, margin: 1 })
      .then((url: string) => {
        if (!cancelled) setQrDataUrl(url);
      })
      .catch(() => {
        if (!cancelled) {
          setQrDataUrl(null);
          setQrFailed(true);
        }
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
    <form onSubmit={handleSubmit} className="aw-stack aw-stack-3">
      <p className="aw-flush aw-text-sm">{t('security.totp.enroll_instructions')}</p>
      {qrDataUrl ? (
        <img
          src={qrDataUrl}
          alt={t('security.totp.qr_label')}
          width={200}
          height={200}
          className="aw-self-center"
        />
      ) : qrFailed ? (
        <p role="alert" className="aw-warning">
          {t('security.qr_fallback_title')}
        </p>
      ) : null}
      <div className="aw-grid-secret aw-surface">
        <span className="aw-text-sm aw-weight-semibold">
          {t('security.totp.manual_secret_label')}
        </span>
        <code className="aw-mono aw-text-sm" data-testid="totp-secret">
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
            onKeyDown={capsHint.handlers.onKeyDown}
            onFocus={capsHint.handlers.onFocus}
            onBlur={capsHint.handlers.onBlur}
            required
          />
        )}
      </FormField>
      {capsHint.capsLockOn ? (
        <output aria-live="polite" className="aw-flush aw-muted aw-text-xs">
          {t('login.caps_lock_on')}
        </output>
      ) : null}
      {errorKey ? (
        <p role="alert" className="aw-error">
          {t(errorKey)}
        </p>
      ) : null}
      <div className="aw-actions">
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
 * @brief Format today's date as YYYY-MM-DD in the user's local time zone.
 *
 * Used both for the printed/exported file header and for the file name
 * suffix. Avoids `toISOString()` so the rendered date matches the user's
 * wall clock instead of UTC.
 */
function todayLocalDate(now: Date = new Date()): string {
  const y = now.getFullYear();
  const m = String(now.getMonth() + 1).padStart(2, '0');
  const d = String(now.getDate()).padStart(2, '0');
  return `${y}-${m}-${d}`;
}

/**
 * @brief Build the textual export of the recovery codes used by both
 *        "Copy all" and "Download .txt" affordances.
 */
function buildRecoveryCodesText(codes: readonly string[], header: string): string {
  return `${header}\n${codes.join('\n')}\n`;
}

/**
 * RecoveryCodesView — shown once after confirm or regenerate. The codes
 * are never returned again, so the user must save them now. Provides
 * Copy / Download / Print affordances so the user can always exfiltrate
 * the secrets even when the browser denies clipboard access.
 *
 * Exported for unit testing; do not import from feature code.
 */
export function RecoveryCodesView({
  codes,
  onDismiss,
}: {
  codes: string[];
  onDismiss: () => void;
}): ReactElement {
  const { t } = useTranslation('auth');
  const date = todayLocalDate();
  const fileHeader = t('security.totp.recovery.file_header', { date });

  const handleCopyAll = async (): Promise<void> => {
    try {
      await navigator.clipboard.writeText(codes.join('\n'));
      toaster.show({ tone: 'success', message: t('security.totp.recovery.copied') });
    } catch {
      toaster.show({ tone: 'danger', message: t('security.totp.recovery.copy_failed') });
    }
  };

  const handleDownload = (): void => {
    const blob = new Blob([buildRecoveryCodesText(codes, fileHeader)], {
      type: 'text/plain;charset=utf-8',
    });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `nodate-flow-recovery-codes-${date}.txt`;
    document.body.appendChild(anchor);
    anchor.click();
    document.body.removeChild(anchor);
    URL.revokeObjectURL(url);
  };

  const handlePrint = (): void => {
    document.body.classList.add('nf-print-recovery-codes');
    try {
      window.print();
    } finally {
      // Restore the regular UI even if the user cancels the print dialog.
      // Some browsers fire `afterprint`, others do not — best effort cleanup
      // happens synchronously here as well.
      document.body.classList.remove('nf-print-recovery-codes');
    }
  };

  return (
    <div className="aw-stack aw-stack-3 aw-print-only-area">
      <h3 className="aw-flush aw-text-base">{t('security.totp.recovery_codes_title')}</h3>
      <p role="alert" className="aw-warning">
        {t('security.totp.recovery_codes_warning')}
      </p>
      <ul className="aw-grid-codes aw-surface aw-mono aw-text-sm">
        {codes.map((c) => (
          <li key={c}>{c}</li>
        ))}
      </ul>
      <div className={recoveryStyles.actions}>
        <Button
          type="button"
          variant="default"
          aria-label={t('security.totp.recovery.copy_all_aria')}
          onClick={() => {
            void handleCopyAll();
          }}
        >
          {t('security.totp.recovery.copy_all')}
        </Button>
        <Button
          type="button"
          variant="default"
          aria-label={t('security.totp.recovery.download_aria')}
          onClick={handleDownload}
        >
          {t('security.totp.recovery.download')}
        </Button>
        <Button
          type="button"
          variant="default"
          aria-label={t('security.totp.recovery.print_aria')}
          onClick={handlePrint}
        >
          {t('security.totp.recovery.print')}
        </Button>
      </div>
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
      <div className="aw-stack aw-stack-3">
        <p className="aw-flush aw-muted aw-text-sm">{t('security.totp.enabled_description')}</p>
        <div className="aw-actions">
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
      className="aw-stack aw-stack-3"
    >
      <p className="aw-flush aw-text-sm">{title}</p>
      <FormField label={t('security.totp.password_required')} required>
        {(control) => (
          <PasswordInput
            {...control}
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
        <p role="alert" className="aw-error">
          {t(errorKey)}
        </p>
      ) : null}
      <div className="aw-actions">
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
