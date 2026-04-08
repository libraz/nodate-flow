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
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  type SettingsApiError,
  type TotpEnrollResponse,
  useTotpConfirm,
  useTotpDisable,
  useTotpEnroll,
  useTotpStatusQuery,
} from './api';

function EnrollmentForm({
  enrollment,
  onConfirmed,
}: {
  enrollment: TotpEnrollResponse;
  onConfirmed: () => void;
}): ReactElement {
  const { t } = useTranslation('settings');
  const confirm = useTotpConfirm();
  const [code, setCode] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    setSubmitting(true);
    try {
      await confirm.mutateAsync(code);
      toaster.show({ tone: 'success', message: t('security.totp.enabled_toast') });
      onConfirmed();
    } catch (err) {
      const apiErr = err as SettingsApiError;
      if (apiErr.code === 'AUTH.TOTP.CODE_MISMATCH') {
        toaster.show({ tone: 'danger', message: t('security.totp.errors.code_mismatch') });
      } else {
        toaster.show({ tone: 'danger', message: t('security.totp.errors.confirm_failed') });
      }
    } finally {
      setSubmitting(false);
    }
  };

  const handleCopy = (): void => {
    void navigator.clipboard.writeText(enrollment.secret).then(() => {
      toaster.show({ tone: 'success', message: t('security.totp.secret_copied') });
    });
  };

  return (
    <form
      onSubmit={(e) => {
        void handleSubmit(e);
      }}
      style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}
    >
      <p style={{ margin: 0 }}>{t('security.totp.enroll_instructions')}</p>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'auto 1fr auto',
          gap: '0.5rem',
          alignItems: 'center',
          padding: '0.75rem 1rem',
          border: '1px solid var(--color-border)',
          borderRadius: '0.5rem',
        }}
      >
        <span style={{ fontWeight: 500 }}>{t('security.totp.secret_label')}</span>
        <code
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: '0.875rem',
            wordBreak: 'break-all',
          }}
        >
          {enrollment.secret}
        </code>
        <Button type="button" variant="ghost" onClick={handleCopy}>
          {t('security.totp.copy')}
        </Button>
      </div>
      <p style={{ margin: 0, fontSize: '0.875rem', color: 'var(--color-fg-muted)' }}>
        <a href={enrollment.otpauthUrl}>{t('security.totp.open_in_app')}</a>
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
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <Button type="submit" variant="primary" disabled={submitting || code.length !== 6}>
          {submitting ? t('security.totp.confirming') : t('security.totp.confirm')}
        </Button>
      </div>
    </form>
  );
}

function DisableForm(): ReactElement {
  const { t } = useTranslation('settings');
  const disable = useTotpDisable();
  const [password, setPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (!window.confirm(t('security.totp.disable_confirm'))) return;
    setSubmitting(true);
    try {
      await disable.mutateAsync(password);
      setPassword('');
      toaster.show({ tone: 'success', message: t('security.totp.disabled_toast') });
    } catch (err) {
      const apiErr = err as SettingsApiError;
      if (apiErr.code === 'AUTH.PASSWORD.CURRENT_MISMATCH') {
        toaster.show({ tone: 'danger', message: t('security.totp.errors.password_mismatch') });
      } else {
        toaster.show({ tone: 'danger', message: t('security.totp.errors.disable_failed') });
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form
      onSubmit={(e) => {
        void handleSubmit(e);
      }}
      style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}
    >
      <p style={{ margin: 0 }}>{t('security.totp.enabled_description')}</p>
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
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
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

  if (status === 'enabled') {
    return <DisableForm />;
  }

  if (enrollment != null || status === 'pending') {
    if (enrollment == null) {
      // pending server-side but we don't have the secret in this tab;
      // offer to regenerate.
      return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
          <p style={{ margin: 0 }}>{t('security.totp.pending_description')}</p>
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
        onConfirmed={() => {
          setEnrollment(null);
        }}
      />
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
      <p style={{ margin: 0 }}>{t('security.totp.disabled_description')}</p>
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
