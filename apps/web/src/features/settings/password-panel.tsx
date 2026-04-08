/**
 * PasswordPanel — change the authenticated user's password. On
 * success every other session is revoked server-side and the user is
 * told how many devices were signed out.
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type SettingsApiError, useChangePassword } from './api';

interface State {
  current: string;
  next: string;
  confirm: string;
}

const EMPTY: State = { current: '', next: '', confirm: '' };

export default function PasswordPanel(): ReactElement {
  const { t } = useTranslation('settings');
  const change = useChangePassword();
  const [state, setState] = useState<State>(EMPTY);
  const [submitting, setSubmitting] = useState(false);
  const [fieldError, setFieldError] = useState<string | null>(null);

  const update = (key: keyof State, value: string): void => {
    setState((prev) => ({ ...prev, [key]: value }));
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    setFieldError(null);
    if (state.next.length < 8) {
      setFieldError(t('security.password.validation.too_short'));
      return;
    }
    if (state.next !== state.confirm) {
      setFieldError(t('security.password.validation.mismatch'));
      return;
    }
    setSubmitting(true);
    try {
      const { otherSessionsRevoked } = await change.mutateAsync({
        currentPassword: state.current,
        newPassword: state.next,
      });
      setState(EMPTY);
      toaster.show({
        tone: 'success',
        message: t('security.password.changed', { count: otherSessionsRevoked }),
      });
    } catch (err) {
      const apiErr = err as SettingsApiError;
      if (apiErr.code === 'AUTH.PASSWORD.CURRENT_MISMATCH') {
        setFieldError(t('security.password.errors.current_mismatch'));
      } else if (apiErr.code === 'AUTH.PASSWORD.NO_LOCAL_IDENTITY') {
        setFieldError(t('security.password.errors.no_local_identity'));
      } else {
        toaster.show({ tone: 'danger', message: t('security.password.errors.generic') });
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
      style={{ display: 'flex', flexDirection: 'column', gap: '1rem', maxInlineSize: '28rem' }}
    >
      <p style={{ margin: 0, color: 'var(--color-fg-muted)' }}>
        {t('security.password.description')}
      </p>
      <FormField label={t('security.password.current')} required>
        {(control) => (
          <Input
            {...control}
            type="password"
            autoComplete="current-password"
            value={state.current}
            onChange={(e) => {
              update('current', e.target.value);
            }}
            required
          />
        )}
      </FormField>
      <FormField label={t('security.password.next')} required>
        {(control) => (
          <Input
            {...control}
            type="password"
            autoComplete="new-password"
            value={state.next}
            onChange={(e) => {
              update('next', e.target.value);
            }}
            minLength={8}
            required
          />
        )}
      </FormField>
      <FormField label={t('security.password.confirm')} required>
        {(control) => (
          <Input
            {...control}
            type="password"
            autoComplete="new-password"
            value={state.confirm}
            onChange={(e) => {
              update('confirm', e.target.value);
            }}
            minLength={8}
            required
          />
        )}
      </FormField>
      {fieldError != null && (
        <p style={{ margin: 0, color: 'var(--color-danger-fg)' }} role="alert">
          {fieldError}
        </p>
      )}
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <Button type="submit" variant="primary" disabled={submitting}>
          {submitting ? t('security.password.saving') : t('security.password.save')}
        </Button>
      </div>
    </form>
  );
}
