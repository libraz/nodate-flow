import type { components } from '@nodate-flow/sdk';
import { brandFileStem } from '@nodate-flow/ui/brand';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import QRCode from 'qrcode';
import { type FormEvent, type ReactElement, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { apiRequest } from '../../lib/api';
import { refusalCode } from '../../lib/auth-errors';
import { APP_NAME } from '../../lib/brand';
import { logError } from '../../lib/log';
import PasswordInput from './password-input';
import recoveryStyles from './recovery-codes.module.css';
import { useCapsLockHint } from './use-caps-lock-hint';

type TotpStatusOutputBody = components['schemas']['TotpStatusOutputBody'];
type TotpStatus = TotpStatusOutputBody['status'];
type TotpEnrollOutputBody = components['schemas']['TotpEnrollOutputBody'];
type TotpConfirmOutputBody = components['schemas']['TotpConfirmOutputBody'];
type TotpRecoveryCodesStatusOutputBody = components['schemas']['TotpRecoveryCodesStatusOutputBody'];
type TotpRegenerateRecoveryCodesOutputBody =
  components['schemas']['TotpRegenerateRecoveryCodesOutputBody'];

/**
 * TotpSection — three-step wizard driven by the server-side
 * `{status: "disabled" | "pending" | "enabled"}` state.
 */
export function TotpSection(): ReactElement {
  const { t } = useTranslation('auth');
  const [status, setStatus] = useState<TotpStatus | null>(null);
  const [enrollment, setEnrollment] = useState<TotpEnrollOutputBody | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [errorKey, setErrorKey] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [startPassword, setStartPassword] = useState('');
  const startPasswordRef = useRef<HTMLInputElement>(null);

  // A refusal is almost always the password; hand focus back to it so the
  // error read with the field is the first thing heard.
  useEffect(() => {
    if (!errorKey) return;
    startPasswordRef.current?.focus();
    startPasswordRef.current?.select();
  }, [errorKey]);

  // Load initial status. The server only returns `status`, not the
  // otpauth URL / secret, so a page reload mid-enrollment drops us
  // back to a "resume" affordance (see pending branch below).
  useEffect(() => {
    let cancelled = false;
    void apiRequest((client) => client.GET('/me/totp'), 'Failed to load two-factor status')
      .then((res) => {
        if (!cancelled && res) {
          const body = res as TotpStatusOutputBody;
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

  const startEnroll = async (password: string): Promise<void> => {
    setErrorKey(null);
    setBusy(true);
    try {
      const data = await apiRequest(
        (client) => client.POST('/me/totp/enroll', { body: { password } }),
        'Failed to start two-factor enrollment',
      );
      const body = data as TotpEnrollOutputBody;
      setEnrollment(body);
      setStatus('pending');
      setStartPassword('');
    } catch (err) {
      setErrorKey(
        refusalCode(err) === 'AUTH.PASSWORD.CURRENT_MISMATCH'
          ? 'security.totp.errors.password_mismatch'
          : 'security.totp.errors.enroll_failed',
      );
    } finally {
      setBusy(false);
    }
  };

  const cancelEnroll = async (password: string): Promise<void> => {
    setErrorKey(null);
    // Clearing a pending secret goes through the same password-gated
    // DELETE /me/totp as a full disable — there is no separate abort
    // endpoint. Never send an empty password: the server rejects it and
    // keeps the pending secret, so optimistically flipping the UI to
    // "disabled" would lie about the account's real state.
    if (password === '') {
      setErrorKey('validation.current_password_required');
      return;
    }
    setBusy(true);
    try {
      await apiRequest(
        (client) => client.DELETE('/me/totp', { body: { password } }),
        'Failed to clear the pending secret',
      );
      setEnrollment(null);
      setStatus('disabled');
    } catch (err) {
      // Keep the real (pending) state — the secret still exists server-side.
      setErrorKey(
        refusalCode(err) === 'AUTH.PASSWORD.CURRENT_MISMATCH'
          ? 'security.totp.errors.password_mismatch'
          : 'security.totp.errors.cancel_failed',
      );
      toaster.show({ message: t('security.totp.errors.cancel_failed'), tone: 'danger' });
    } finally {
      setBusy(false);
    }
  };

  const confirmEnroll = async (code: string, password: string): Promise<void> => {
    setErrorKey(null);
    setBusy(true);
    try {
      const data = await apiRequest(
        (client) => client.POST('/me/totp/confirm', { body: { code, password } }),
        'Failed to confirm two-factor enrollment',
      );
      const body = data as TotpConfirmOutputBody;
      setRecoveryCodes(body.recoveryCodes ?? []);
      setEnrollment(null);
      setStatus('enabled');
    } catch (err) {
      const errCode = refusalCode(err);
      if (errCode === 'AUTH.TOTP.CODE_MISMATCH') {
        setErrorKey('security.totp.errors.code_mismatch');
      } else if (errCode === 'AUTH.PASSWORD.CURRENT_MISMATCH') {
        setErrorKey('security.totp.errors.password_mismatch');
      } else {
        setErrorKey('security.totp.errors.confirm_failed');
      }
    } finally {
      setBusy(false);
    }
  };

  if (status === null) {
    return (
      <div className="aw-stack aw-stack-3" aria-busy="true">
        <Skeleton />
      </div>
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
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (busy || startPassword === '') return;
          void startEnroll(startPassword);
        }}
        noValidate
        className="aw-stack aw-stack-3"
      >
        <p className="aw-flush aw-muted aw-text-sm">{t('security.totp.disabled_description')}</p>
        <FormField
          label={t('security.totp.password_required')}
          required
          {...(errorKey ? { error: t(errorKey) } : {})}
        >
          {(control) => (
            <PasswordInput
              {...control}
              ref={startPasswordRef}
              value={startPassword}
              autoComplete="current-password"
              onChange={(e) => {
                setStartPassword(e.target.value);
              }}
              required
            />
          )}
        </FormField>
        <div>
          <Button
            type="submit"
            variant="primary"
            aria-busy={busy || undefined}
            disabled={busy || startPassword === ''}
          >
            {t('security.totp.start_setup')}
          </Button>
        </div>
      </form>
    );
  }

  if (status === 'pending') {
    if (enrollment === null) {
      // Page reloaded mid-enrollment: the server never returns the
      // otpauth URL / secret on GET /me/totp, so we cannot resume
      // the QR step. Offer to restart (POST /me/totp/enroll is
      // idempotent in the pending state and rotates the secret).
      return (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (busy || startPassword === '') return;
            void startEnroll(startPassword);
          }}
          noValidate
          className="aw-stack aw-stack-3"
        >
          <p className="aw-flush aw-muted aw-text-sm">{t('security.totp.pending_description')}</p>
          <FormField
            label={t('security.totp.password_required')}
            required
            {...(errorKey ? { error: t(errorKey) } : {})}
          >
            {(control) => (
              <PasswordInput
                {...control}
                ref={startPasswordRef}
                value={startPassword}
                autoComplete="current-password"
                onChange={(e) => {
                  setStartPassword(e.target.value);
                }}
                required
              />
            )}
          </FormField>
          <div className="aw-actions">
            <Button
              type="submit"
              variant="primary"
              aria-busy={busy || undefined}
              disabled={busy || startPassword === ''}
            >
              {t('security.totp.resume_restart')}
            </Button>
            <Button
              type="button"
              variant="ghost"
              disabled={busy}
              onClick={() => {
                void cancelEnroll(startPassword);
              }}
            >
              {t('security.totp.cancel')}
            </Button>
          </div>
        </form>
      );
    }
    return (
      <EnrollmentStep
        enrollment={enrollment}
        busy={busy}
        errorKey={errorKey}
        onConfirm={(code, password) => {
          void confirmEnroll(code, password);
        }}
        onCancel={(password) => {
          void cancelEnroll(password);
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
  onConfirm: (code: string, password: string) => void;
  onCancel: (password: string) => void;
}): ReactElement {
  const { t } = useTranslation('auth');
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
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
    if (code.length === 6 && password !== '') onConfirm(code, password);
  };

  return (
    <form onSubmit={handleSubmit} className="aw-stack aw-stack-3 aw-step-forward">
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
            className="aw-otp"
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9]{6}"
            maxLength={6}
            autoFocus
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
      <FormField label={t('security.totp.password_required')} required>
        {(control) => (
          <PasswordInput
            {...control}
            value={password}
            autoComplete="current-password"
            onChange={(e) => {
              setPassword(e.target.value);
            }}
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
        <Button
          type="submit"
          variant="primary"
          aria-busy={busy || undefined}
          disabled={busy || code.length !== 6 || password === ''}
        >
          {t('security.totp.confirm')}
        </Button>
        <Button
          type="button"
          variant="ghost"
          disabled={busy}
          onClick={() => {
            onCancel(password);
          }}
        >
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
  const fileHeader = t('security.totp.recovery.file_header', { date, appName: APP_NAME });

  const handleCopyAll = async (): Promise<void> => {
    try {
      await navigator.clipboard.writeText(codes.join('\n'));
      toaster.show({ tone: 'success', message: t('security.totp.recovery.copied') });
    } catch {
      // error-toast-exempt: the clipboard write never reached the API, so the
      // rejection carries a browser permission state and no refusal to show.
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
    anchor.download = `${brandFileStem(APP_NAME)}-recovery-codes-${date}.txt`;
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
    <div className="aw-stack aw-stack-3 aw-print-only-area aw-step-forward">
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
  const [remaining, setRemaining] = useState<number | null>(null);
  const passwordRef = useRef<HTMLInputElement>(null);

  // Remounts after a regenerate, so the count is re-read then.
  useEffect(() => {
    let cancelled = false;
    void apiRequest(
      (client) => client.GET('/me/totp/recovery-codes'),
      'Failed to load the recovery code count',
    )
      .then((res) => {
        if (!cancelled) setRemaining((res as TotpRecoveryCodesStatusOutputBody).remaining);
      })
      .catch((err: unknown) => {
        if (!cancelled) logError('Failed to fetch recovery code count', err);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!errorKey) return;
    passwordRef.current?.focus();
    passwordRef.current?.select();
  }, [errorKey]);

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
      await apiRequest(
        (client) => client.DELETE('/me/totp', { body: { password } }),
        'Failed to turn off two-factor authentication',
      );
      resetForm();
      onDisabled();
    } catch (err) {
      if (refusalCode(err) === 'AUTH.PASSWORD.CURRENT_MISMATCH') {
        setErrorKey('security.totp.errors.password_mismatch');
      } else {
        setErrorKey('security.totp.errors.disable_failed');
      }
    } finally {
      setBusy(false);
    }
  };

  const handleRegenerate = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    setErrorKey(null);
    setBusy(true);
    try {
      const data = await apiRequest(
        (client) => client.POST('/me/totp/recovery-codes', { body: { password } }),
        'Failed to regenerate recovery codes',
      );
      const body = data as TotpRegenerateRecoveryCodesOutputBody;
      resetForm();
      onRegenerated(body.recoveryCodes ?? []);
    } catch (err) {
      if (refusalCode(err) === 'AUTH.PASSWORD.CURRENT_MISMATCH') {
        setErrorKey('security.totp.errors.password_mismatch');
      } else {
        setErrorKey('security.totp.errors.regenerate_failed');
      }
    } finally {
      setBusy(false);
    }
  };

  if (mode === 'idle') {
    return (
      <div className="aw-stack aw-stack-3">
        <p className="aw-flush aw-muted aw-text-sm">{t('security.totp.enabled_description')}</p>
        {remaining !== null ? (
          <p className="aw-flush aw-text-sm" data-testid="recovery-remaining">
            {t('security.totp.recovery.remaining', { count: remaining })}
          </p>
        ) : null}
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
      noValidate
      className="aw-stack aw-stack-3 aw-step-forward"
    >
      <p className="aw-flush aw-text-sm">{title}</p>
      <FormField
        label={t('security.totp.password_required')}
        required
        {...(errorKey ? { error: t(errorKey) } : {})}
      >
        {(control) => (
          <PasswordInput
            {...control}
            ref={passwordRef}
            autoComplete="current-password"
            autoFocus
            value={password}
            onChange={(e) => {
              setPassword(e.target.value);
            }}
            required
          />
        )}
      </FormField>
      <div className="aw-actions">
        <Button
          type="submit"
          variant={mode === 'disable' ? 'danger' : 'primary'}
          aria-busy={busy || undefined}
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
