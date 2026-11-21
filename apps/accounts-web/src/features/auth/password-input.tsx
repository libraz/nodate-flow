/**
 * PasswordInput — masked text field with a show/hide toggle.
 *
 * Lives at the feature level (not in `packages/ui`) because the toggle
 * copy + `aria-pressed` semantics are auth-specific and only the auth
 * flows currently render password fields.
 *
 * CapsLock is reported to the surrounding {@link PasswordField}, which shows
 * the hint in the form field's description slot so it is read with the field.
 */

import FormField, {
  type FormFieldControlProps,
  type FormFieldProps,
} from '@nodate-flow/ui/primitives/form-field';
import Input, { type InputProps } from '@nodate-flow/ui/primitives/input';
import {
  createContext,
  forwardRef,
  type KeyboardEvent,
  type ReactElement,
  useContext,
  useId,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import CapsLockHint, { describedByWith } from './caps-lock-hint';
import styles from './password-input.module.css';

type CapsReporter = (capsOn: boolean) => void;

const CapsReporterContext = createContext<CapsReporter | null>(null);

/**
 * FormField for a password input: while the input inside it has focus and
 * CapsLock is on, a warning eases open below the control.
 */
export function PasswordField({ children, ...rest }: FormFieldProps): ReactElement {
  const [capsOn, setCapsOn] = useState(false);
  const hintId = useId();
  return (
    <CapsReporterContext value={setCapsOn}>
      <FormField {...rest}>
        {(control) => (
          <>
            {children(withHint(control, hintId, capsOn))}
            <CapsLockHint open={capsOn} id={hintId} />
          </>
        )}
      </FormField>
    </CapsReporterContext>
  );
}

function withHint(
  control: FormFieldControlProps,
  hintId: string,
  open: boolean,
): FormFieldControlProps {
  const merged = describedByWith(control['aria-describedby'], hintId, open);
  const { 'aria-describedby': _base, ...rest } = control;
  return merged ? { ...rest, 'aria-describedby': merged } : rest;
}

export type PasswordInputProps = Omit<InputProps, 'type'>;

/** PasswordInput renders a native password field plus a sibling show/hide toggle. */
const PasswordInput = forwardRef<HTMLInputElement, PasswordInputProps>(
  ({ onBlur, onKeyDown, ...rest }, ref): ReactElement => {
    const { t } = useTranslation('auth');
    const [revealed, setRevealed] = useState(false);
    const reportCaps = useContext(CapsReporterContext);

    const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>): void => {
      // `getModifierState` asks the OS whether CapsLock is on; watching for
      // shifted letters in `value` is unreliable on non-Latin layouts.
      reportCaps?.(event.getModifierState('CapsLock'));
      onKeyDown?.(event);
    };

    return (
      <span className={styles.wrapper}>
        <Input
          {...rest}
          ref={ref}
          type={revealed ? 'text' : 'password'}
          dir="ltr"
          onBlur={(event) => {
            // Clear the hint so it does not return stale when the field is refocused.
            reportCaps?.(false);
            onBlur?.(event);
          }}
          onKeyDown={handleKeyDown}
          className={styles.input}
        />
        <button
          type="button"
          aria-pressed={revealed}
          aria-label={revealed ? t('login.password_hide') : t('login.password_show')}
          onClick={() => {
            setRevealed((prev) => !prev);
          }}
          className={`${styles.toggle} nf-focus-ring-inset`}
        >
          {revealed ? t('login.password_hide') : t('login.password_show')}
        </button>
      </span>
    );
  },
);
PasswordInput.displayName = 'PasswordInput';

export default PasswordInput;
