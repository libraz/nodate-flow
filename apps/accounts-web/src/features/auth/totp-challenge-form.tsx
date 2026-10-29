/**
 * TotpChallengeForm — the second-factor step shared by password sign-in,
 * magic-link sign-in and OIDC sign-in.
 *
 * Owns the code / recovery-code entry and its validation; the page owns
 * the challenge token, the submit guard and the request, and receives the
 * entered credential through `onSubmit`.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import {
  type FormEvent,
  type ReactElement,
  type ReactNode,
  useEffect,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import type { AuthErrorI18nKey } from '../../lib/auth-errors';
import { useCapsLockHint } from './use-caps-lock-hint';

/**
 * Recovery codes are issued as 10-character base32 strings; up to 20 are
 * accepted so a longer future format is not silently truncated. Mirrored
 * in the `login.recovery_helper` copy.
 */
const RECOVERY_MIN_LEN = 8;
const RECOVERY_MAX_LEN = 20;
const TOTP_LEN = 6;

export type TotpCredential = { code: string } | { recoveryCode: string };

export interface TotpChallengeFormProps {
  /** Refusal from the last attempt, rendered against the field. */
  error: AuthErrorI18nKey | null;
  /** A submission is in flight. */
  submitting: boolean;
  /** Receives the entered credential once it is well-formed. */
  onSubmit: (credential: TotpCredential) => void;
  /** Called when the reader switches between a code and a recovery code. */
  onModeChange: () => void;
  /** Way out of the challenge (a button or a link), rendered last. */
  cancel?: ReactNode;
  className?: string;
}

export default function TotpChallengeForm({
  error,
  submitting,
  onSubmit,
  onModeChange,
  cancel,
  className,
}: TotpChallengeFormProps): ReactElement {
  const { t } = useTranslation('auth');
  const [useRecovery, setUseRecovery] = useState(false);
  const [modeShift, setModeShift] = useState<'forward' | 'back' | null>(null);
  const [totpCode, setTotpCode] = useState('');
  const [recoveryCode, setRecoveryCode] = useState('');
  const capsHint = useCapsLockHint();
  const inputRef = useRef<HTMLInputElement>(null);

  // A refused code leaves focus on the (now re-enabled) submit button;
  // put it back on the field the refusal is attached to, ready to retype.
  useEffect(() => {
    if (!error) return;
    inputRef.current?.focus();
    inputRef.current?.select();
  }, [error]);

  const trimmedRecovery = recoveryCode.trim();
  const complete = useRecovery
    ? trimmedRecovery.length >= RECOVERY_MIN_LEN
    : totpCode.length === TOTP_LEN;

  const handleSubmit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    if (!complete) return;
    onSubmit(useRecovery ? { recoveryCode: trimmedRecovery } : { code: totpCode });
  };

  const handleToggleMode = (): void => {
    setModeShift(useRecovery ? 'back' : 'forward');
    setUseRecovery((v) => !v);
    onModeChange();
  };

  // One status string at a time, so the description read with the field
  // never contradicts itself; the disabled submit button is the second cue.
  const status = complete
    ? t('login.totp_status_awaiting')
    : useRecovery
      ? t('login.recovery_helper')
      : t('login.totp_status_need_digits');
  const errorText = error ? t(error) : null;
  const fieldShift =
    modeShift === 'forward' ? 'aw-step-forward' : modeShift === 'back' ? 'aw-step-back' : null;

  return (
    <form onSubmit={handleSubmit} noValidate className={cx('aw-stack aw-stack-5', className)}>
      <div className="aw-stack aw-stack-2">
        <h1 className="aw-page-title">{t('login.totp_title')}</h1>
        <p className="aw-flush aw-muted aw-text-sm">{t('login.totp_instructions')}</p>
      </div>
      {useRecovery ? (
        <FormField
          key="recovery"
          className={fieldShift ?? undefined}
          label={t('login.recovery_code')}
          required
          description={status}
          {...(errorText ? { error: errorText } : {})}
        >
          {(control) => (
            <Input
              {...control}
              ref={inputRef}
              className="aw-otp"
              autoComplete="one-time-code"
              autoCapitalize="characters"
              spellCheck={false}
              enterKeyHint="go"
              maxLength={RECOVERY_MAX_LEN}
              value={recoveryCode}
              onChange={(e) => {
                setRecoveryCode(e.target.value.toUpperCase().slice(0, RECOVERY_MAX_LEN));
              }}
              onKeyDown={capsHint.handlers.onKeyDown}
              onFocus={capsHint.handlers.onFocus}
              onBlur={capsHint.handlers.onBlur}
              autoFocus
            />
          )}
        </FormField>
      ) : (
        <FormField
          key="code"
          className={fieldShift ?? undefined}
          label={t('login.totp_code')}
          required
          description={status}
          {...(errorText ? { error: errorText } : {})}
        >
          {(control) => (
            <Input
              {...control}
              ref={inputRef}
              className="aw-otp"
              inputMode="numeric"
              autoComplete="one-time-code"
              enterKeyHint="go"
              pattern="[0-9]{6}"
              maxLength={TOTP_LEN}
              value={totpCode}
              onChange={(e) => {
                // Keeps only the digits, so a pasted "123 456" lands whole.
                setTotpCode(e.target.value.replace(/\D/g, '').slice(0, TOTP_LEN));
              }}
              onKeyDown={capsHint.handlers.onKeyDown}
              onFocus={capsHint.handlers.onFocus}
              onBlur={capsHint.handlers.onBlur}
              autoFocus
            />
          )}
        </FormField>
      )}
      {capsHint.capsLockOn ? (
        <output aria-live="polite" className="aw-flush aw-muted aw-text-xs">
          {t('login.caps_lock_on')}
        </output>
      ) : null}
      <div className="aw-stack aw-stack-2">
        <Button
          type="submit"
          variant="primary"
          aria-busy={submitting || undefined}
          disabled={submitting || !complete}
        >
          {useRecovery ? t('login.recovery_submit') : t('login.totp_submit')}
        </Button>
        <Button type="button" variant="ghost" onClick={handleToggleMode} disabled={submitting}>
          {useRecovery ? t('login.totp_use_code') : t('login.totp_use_recovery')}
        </Button>
        {cancel}
      </div>
    </form>
  );
}
