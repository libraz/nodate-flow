/**
 * useCapsLockHint — small helper that returns whether CapsLock is on for
 * the focused field plus the keydown / focus / blur handlers needed to
 * keep that flag in sync.
 *
 * Used by code-entry inputs (TOTP, recovery code) where CapsLock matters
 * for alphanumeric characters but the host field still wants its own
 * onChange behaviour. Centralizing the logic avoids duplicating the
 * `getModifierState('CapsLock')` plumbing across each form.
 */

import { type FocusEventHandler, type KeyboardEventHandler, useCallback, useState } from 'react';

export interface CapsLockHint {
  /** True while the field is focused AND CapsLock is on. */
  capsLockOn: boolean;
  /** Spread these onto the field that should track CapsLock state. */
  handlers: {
    onKeyDown: KeyboardEventHandler<HTMLInputElement>;
    onFocus: FocusEventHandler<HTMLInputElement>;
    onBlur: FocusEventHandler<HTMLInputElement>;
  };
}

/** Track whether CapsLock is currently on for the focused field. */
export function useCapsLockHint(): CapsLockHint {
  const [focused, setFocused] = useState(false);
  const [capsOn, setCapsOn] = useState(false);

  const onKeyDown = useCallback<KeyboardEventHandler<HTMLInputElement>>((event) => {
    setCapsOn(event.getModifierState('CapsLock'));
  }, []);

  const onFocus = useCallback<FocusEventHandler<HTMLInputElement>>(() => {
    setFocused(true);
  }, []);

  const onBlur = useCallback<FocusEventHandler<HTMLInputElement>>(() => {
    setFocused(false);
    setCapsOn(false);
  }, []);

  return {
    capsLockOn: focused && capsOn,
    handlers: { onKeyDown, onFocus, onBlur },
  };
}
