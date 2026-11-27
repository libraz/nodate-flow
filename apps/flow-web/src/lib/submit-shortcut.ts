/**
 * Cmd+Enter / Ctrl+Enter submission for forms with a multiline field.
 *
 * Put {@link onSubmitShortcut} on the `<form>`: the chord submits through the
 * form's enabled submit button, so validation, pending guards and the
 * form's own `onSubmit` run exactly as for a click.
 */

import type { KeyboardEvent } from 'react';

/** True when the key event is the submit chord and no input method owns it. */
export function isSubmitShortcut(event: KeyboardEvent<HTMLElement>): boolean {
  if (event.key !== 'Enter' || !(event.metaKey || event.ctrlKey)) return false;
  // keyCode 229 covers Safari, which fires keydown after compositionend.
  return !(event.nativeEvent.isComposing || event.keyCode === 229);
}

/**
 * Form-level `onKeyDown`: submits via the first enabled submit button.
 * Does nothing when the key was already handled (for example by the `@`
 * mention picker) or when the form has no enabled submit button.
 */
export function onSubmitShortcut(event: KeyboardEvent<HTMLFormElement>): void {
  if (event.defaultPrevented || !isSubmitShortcut(event)) return;
  const form = event.currentTarget;
  const button = Array.from(form.elements).find(
    (el): el is HTMLButtonElement =>
      el instanceof HTMLButtonElement && el.type === 'submit' && !el.disabled,
  );
  if (!button) return;
  event.preventDefault();
  form.requestSubmit(button);
}

/** True on Apple platforms, where the chord is Cmd rather than Ctrl. */
export function isApplePlatform(): boolean {
  return typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);
}

/** Keycap labels for the chord, matching {@link isApplePlatform}. */
export function submitShortcutKeys(): readonly string[] {
  return isApplePlatform() ? ['⌘', 'Enter'] : ['Ctrl', 'Enter'];
}
