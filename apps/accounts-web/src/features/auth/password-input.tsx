/**
 * PasswordInput — masked text field with a show/hide toggle and an
 * accessible CapsLock-on hint that surfaces while the field is focused.
 *
 * Lives at the feature level (not in `packages/ui`) because the toggle
 * copy + `aria-pressed` semantics are auth-specific and only the auth
 * flows currently render password fields. Promote to a primitive once a
 * second consumer appears.
 */

import Input, { type InputProps } from '@nodate-flow/ui/primitives/input';
import {
  type ChangeEvent,
  type FocusEvent,
  type KeyboardEvent,
  type ReactElement,
  forwardRef,
  useCallback,
  useId,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

export interface PasswordInputProps extends Omit<InputProps, 'type'> {
  /** Called whenever the underlying input value changes. */
  onChange?: (event: ChangeEvent<HTMLInputElement>) => void;
}

/**
 * PasswordInput renders a native password field plus a sibling button to
 * toggle between masked / unmasked rendering, and a polite live region
 * that announces "CapsLock is on" while the field has focus.
 */
const PasswordInput = forwardRef<HTMLInputElement, PasswordInputProps>(
  (
    { onChange, onFocus, onBlur, onKeyDown, 'aria-describedby': describedBy, ...rest },
    ref,
  ): ReactElement => {
    const { t } = useTranslation('auth');
    const [revealed, setRevealed] = useState(false);
    const [capsOn, setCapsOn] = useState(false);
    const [focused, setFocused] = useState(false);
    const capsHintId = useId();

    const detectCaps = useCallback((event: KeyboardEvent<HTMLInputElement>): void => {
      // `getModifierState` is the canonical way to ask the OS whether the
      // CapsLock LED is on; the alternative (watching for shifted letters
      // in `value`) is unreliable on non-Latin keyboard layouts.
      setCapsOn(event.getModifierState('CapsLock'));
    }, []);

    const handleFocus = useCallback(
      (event: FocusEvent<HTMLInputElement>): void => {
        setFocused(true);
        onFocus?.(event);
      },
      [onFocus],
    );

    const handleBlur = useCallback(
      (event: FocusEvent<HTMLInputElement>): void => {
        setFocused(false);
        // Reset the hint so it does not flash back as a stale state when
        // the field is refocused.
        setCapsOn(false);
        onBlur?.(event);
      },
      [onBlur],
    );

    const handleKeyDown = useCallback(
      (event: KeyboardEvent<HTMLInputElement>): void => {
        detectCaps(event);
        onKeyDown?.(event);
      },
      [detectCaps, onKeyDown],
    );

    const showCapsHint = focused && capsOn;
    const mergedDescribedBy = [describedBy, showCapsHint ? capsHintId : null]
      .filter(Boolean)
      .join(' ')
      .trim();

    return (
      <span style={{ position: 'relative', display: 'inline-flex', width: '100%' }}>
        <Input
          {...rest}
          ref={ref}
          type={revealed ? 'text' : 'password'}
          dir="ltr"
          onChange={onChange}
          onFocus={handleFocus}
          onBlur={handleBlur}
          onKeyDown={handleKeyDown}
          {...(mergedDescribedBy ? { 'aria-describedby': mergedDescribedBy } : {})}
          style={{ paddingInlineEnd: '2.75rem', width: '100%' }}
        />
        <button
          type="button"
          aria-pressed={revealed}
          aria-label={revealed ? t('login.password_hide') : t('login.password_show')}
          onClick={() => {
            setRevealed((prev) => !prev);
          }}
          style={{
            position: 'absolute',
            insetInlineEnd: '0.25rem',
            insetBlockStart: '50%',
            transform: 'translateY(-50%)',
            background: 'transparent',
            border: 'none',
            padding: 'var(--nf-space-1) var(--nf-space-2)',
            cursor: 'pointer',
            color: 'var(--nf-color-fg-muted)',
            font: 'inherit',
            fontSize: 'var(--nf-text-xs)',
          }}
        >
          {revealed ? t('login.password_hide') : t('login.password_show')}
        </button>
        {showCapsHint ? (
          <output
            id={capsHintId}
            aria-live="polite"
            style={{
              marginBlockStart: 'var(--nf-space-1)',
              color: 'var(--nf-color-warning, var(--nf-color-fg-muted))',
              fontSize: 'var(--nf-text-xs)',
              position: 'absolute',
              insetBlockStart: '100%',
              insetInlineStart: 0,
            }}
          >
            {t('login.caps_lock_on')}
          </output>
        ) : null}
      </span>
    );
  },
);
PasswordInput.displayName = 'PasswordInput';

export default PasswordInput;
