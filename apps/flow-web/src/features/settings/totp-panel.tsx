/**
 * TotpPanel — /settings/security TOTP 2FA management. Suspense mode.
 *
 * State machine:
 *   disabled  → "Enable 2FA" button → calls enroll → moves to enrolling
 *   enrolling → shows secret + code input → calls confirm → moves to enabled
 *   pending   → server-side "has secret but never confirmed": same UI as
 *               enrolling, we reuse the secret by calling enroll again
 *               (idempotent rotate)
 *   enabled   → shows "2FA is on" + disable form (password reverify)
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import QRCode from 'qrcode';
import { type FormEvent, type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import {
  type SettingsApiError,
  type TotpEnrollResponse,
  useRecoveryCodesStatusQuery,
  useRegenerateRecoveryCodes,
  useTotpConfirm,
  useTotpDisable,
  useTotpEnroll,
  useTotpStatusQuery,
} from './api';
import styles from './totp-panel.module.css';
import { usePanelSubmission } from './use-panel-submission';

function RecoveryCodesView({
  codes,
  onDismiss,
}: { codes: string[]; onDismiss: () => void }): ReactElement {
  const { t } = useTranslation('settings');
  const handleCopyAll = (): void => {
    navigator.clipboard.writeText(codes.join('\n')).then(
      () => toaster.show({ tone: 'success', message: t('security.totp.recovery.copied') }),
      () => toaster.show({ tone: 'danger', message: t('security.totp.recovery.copy_failed') }),
    );
  };
  const handleDownload = (): void => {
    const blob = new Blob([`${codes.join('\n')}\n`], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'nodate-flow-recovery-codes.txt';
    a.click();
    URL.revokeObjectURL(url);
  };
  return (
    <div className={styles.column}>
      <p className={styles.bareParagraph}>{t('security.totp.recovery.description')}</p>
      <ul className={styles.recoveryList}>
        {codes.map((c) => (
          <li key={c}>{c}</li>
        ))}
      </ul>
      <div className={styles.buttonRow}>
        <Button type="button" variant="ghost" onClick={handleCopyAll}>
          {t('security.totp.recovery.copy_all')}
        </Button>
        <Button type="button" variant="ghost" onClick={handleDownload}>
          {t('security.totp.recovery.download')}
        </Button>
        <Button type="button" variant="primary" onClick={onDismiss}>
          {t('security.totp.recovery.saved')}
        </Button>
      </div>
    </div>
  );
}

function EnrollmentForm({
  enrollment,
  onConfirmed,
}: {
  enrollment: TotpEnrollResponse;
  onConfirmed: (recoveryCodes: string[]) => void;
}): ReactElement {
  const { t } = useTranslation('settings');
  const confirm = useTotpConfirm();
  const [code, setCode] = useState('');
  const { submitting, run } = usePanelSubmission();
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

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    await run(
      async () => {
        const result = await confirm.mutateAsync(code);
        toaster.show({ tone: 'success', message: t('security.totp.enabled_toast') });
        onConfirmed(result.recoveryCodes);
      },
      (err) => {
        const apiErr = err as SettingsApiError;
        if (apiErr.code === 'AUTH.TOTP.CODE_MISMATCH') {
          toaster.show({ tone: 'danger', message: t('security.totp.errors.code_mismatch') });
        } else {
          toaster.show({ tone: 'danger', message: t('security.totp.errors.confirm_failed') });
        }
      },
    );
  };

  const handleCopy = (): void => {
    navigator.clipboard.writeText(enrollment.secret).then(
      () => toaster.show({ tone: 'success', message: t('security.totp.secret_copied') }),
      () => toaster.show({ tone: 'danger', message: t('security.totp.secret_copy_failed') }),
    );
  };

  return (
    <form
      onSubmit={(e) => {
        void handleSubmit(e);
      }}
      className={styles.column}
    >
      <p className={styles.bareParagraph}>{t('security.totp.enroll_instructions')}</p>
      {qrDataUrl ? (
        <img
          src={qrDataUrl}
          alt={t('security.totp.qr_alt')}
          width={200}
          height={200}
          className={styles.qrImage}
        />
      ) : null}
      <div className={styles.secretRow}>
        <span className={styles.secretLabel}>{t('security.totp.secret_label')}</span>
        <code className={styles.secretCode}>{enrollment.secret}</code>
        <Button type="button" variant="ghost" onClick={handleCopy}>
          {t('security.totp.copy')}
        </Button>
      </div>
      <p className={styles.helpParagraph}>
        <a href={enrollment.otpauthUrl.startsWith('otpauth:') ? enrollment.otpauthUrl : undefined}>
          {t('security.totp.open_in_app')}
        </a>
      </p>
      <FormField label={t('security.totp.code_label')} required>
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
      <div className={styles.endActions}>
        <Button type="submit" variant="primary" disabled={submitting || code.length !== 6}>
          {submitting ? t('security.totp.confirming') : t('security.totp.confirm')}
        </Button>
      </div>
    </form>
  );
}

function EnabledPanel(): ReactElement {
  const { t } = useTranslation('settings');
  const { data: remaining } = useRecoveryCodesStatusQuery();
  const regenerate = useRegenerateRecoveryCodes();
  const [password, setPassword] = useState('');
  const { submitting, run } = usePanelSubmission();
  const [newCodes, setNewCodes] = useState<string[] | null>(null);

  if (newCodes != null) {
    return (
      <RecoveryCodesView
        codes={newCodes}
        onDismiss={() => {
          setNewCodes(null);
        }}
      />
    );
  }

  const handleRegenerate = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (!(await confirmAction({ message: t('security.totp.recovery.regenerate_confirm') }))) return;
    await run(
      async () => {
        const result = await regenerate.mutateAsync(password);
        setPassword('');
        setNewCodes(result.recoveryCodes);
        toaster.show({ tone: 'success', message: t('security.totp.recovery.regenerated_toast') });
      },
      (err) => {
        const apiErr = err as SettingsApiError;
        if (apiErr.code === 'AUTH.PASSWORD.CURRENT_MISMATCH') {
          toaster.show({ tone: 'danger', message: t('security.totp.errors.password_mismatch') });
        } else {
          toaster.show({
            tone: 'danger',
            message: t('security.totp.recovery.errors.regenerate_failed'),
          });
        }
      },
    );
  };

  return (
    <div className={styles.columnGapMd}>
      <DisableForm />
      <hr className={styles.divider} />
      <div>
        <h3 className={styles.recoveryHeading}>{t('security.totp.recovery.title')}</h3>
        <p className={styles.recoveryRemaining}>
          {t('security.totp.recovery.remaining', { count: remaining })}
        </p>
        <form
          onSubmit={(e) => {
            void handleRegenerate(e);
          }}
          className={styles.formNarrow}
        >
          <FormField label={t('security.totp.recovery.regenerate_password_label')} required>
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
          <div className={styles.endActions}>
            <Button type="submit" variant="default" disabled={submitting || password === ''}>
              {t('security.totp.recovery.regenerate')}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}

function DisableForm(): ReactElement {
  const { t } = useTranslation('settings');
  const disable = useTotpDisable();
  const [password, setPassword] = useState('');
  const { submitting, run } = usePanelSubmission();

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (!(await confirmAction({ message: t('security.totp.disable_confirm') }))) return;
    await run(
      async () => {
        await disable.mutateAsync(password);
        setPassword('');
        toaster.show({ tone: 'success', message: t('security.totp.disabled_toast') });
      },
      (err) => {
        const apiErr = err as SettingsApiError;
        if (apiErr.code === 'AUTH.PASSWORD.CURRENT_MISMATCH') {
          toaster.show({ tone: 'danger', message: t('security.totp.errors.password_mismatch') });
        } else {
          toaster.show({ tone: 'danger', message: t('security.totp.errors.disable_failed') });
        }
      },
    );
  };

  return (
    <form
      onSubmit={(e) => {
        void handleSubmit(e);
      }}
      className={styles.formNarrow}
    >
      <p className={styles.bareParagraph}>{t('security.totp.enabled_description')}</p>
      <FormField label={t('security.totp.password_label')} required>
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
      <div className={styles.endActions}>
        <Button type="submit" variant="danger" disabled={submitting || password === ''}>
          {submitting ? t('security.totp.disabling') : t('security.totp.disable')}
        </Button>
      </div>
    </form>
  );
}

export default function TotpPanel(): ReactElement {
  const { t } = useTranslation('settings');
  const { data: status } = useTotpStatusQuery();
  const enroll = useTotpEnroll();
  const [enrollment, setEnrollment] = useState<TotpEnrollResponse | null>(null);
  const [postConfirmCodes, setPostConfirmCodes] = useState<string[] | null>(null);

  const handleEnroll = async (): Promise<void> => {
    try {
      const data = await enroll.mutateAsync();
      setEnrollment(data);
    } catch (err) {
      const apiErr = err as SettingsApiError;
      if (apiErr.code === 'AUTH.TOTP.NOT_CONFIGURED') {
        toaster.show({ tone: 'danger', message: t('security.totp.errors.not_configured') });
      } else {
        toaster.show({ tone: 'danger', message: t('security.totp.errors.enroll_failed') });
      }
    }
  };

  if (postConfirmCodes != null) {
    return (
      <RecoveryCodesView
        codes={postConfirmCodes}
        onDismiss={() => {
          setPostConfirmCodes(null);
        }}
      />
    );
  }

  if (status === 'enabled') {
    return <EnabledPanel />;
  }

  if (enrollment != null || status === 'pending') {
    if (enrollment == null) {
      // pending server-side but we don't have the secret in this tab;
      // offer to regenerate.
      return (
        <div className={styles.column}>
          <p className={styles.bareParagraph}>{t('security.totp.pending_description')}</p>
          <div>
            <Button
              type="button"
              variant="primary"
              onClick={() => {
                void handleEnroll();
              }}
              disabled={enroll.isPending}
            >
              {t('security.totp.resume_enroll')}
            </Button>
          </div>
        </div>
      );
    }
    return (
      <EnrollmentForm
        enrollment={enrollment}
        onConfirmed={(codes) => {
          setEnrollment(null);
          setPostConfirmCodes(codes);
        }}
      />
    );
  }

  return (
    <div className={styles.column}>
      <p className={styles.bareParagraph}>{t('security.totp.disabled_description')}</p>
      <div>
        <Button
          type="button"
          variant="primary"
          onClick={() => {
            void handleEnroll();
          }}
          disabled={enroll.isPending}
        >
          {t('security.totp.enable')}
        </Button>
      </div>
    </div>
  );
}
