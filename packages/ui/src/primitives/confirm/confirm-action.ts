/**
 * confirmAction — themed-confirm helper that consumers may call from any
 * imperative handler.
 *
 * The primitive is i18n-agnostic: callers always pass an already-translated
 * `message`. Default labels for `title`, `confirmLabel` and `cancelLabel`
 * resolve through a registered label provider (see `setConfirmActionLabels`).
 * Each app should call `setConfirmActionLabels` once at boot — typically
 * right after the i18next instance is initialized — to inject locale-aware
 * fallbacks. If no provider is registered, English defaults are used.
 */

import type { ReactNode } from 'react';
import { type ConfirmTone, confirm } from './confirm';

export interface ConfirmActionOptions {
  /** Already-translated body message. */
  message: ReactNode;
  /** Already-translated title. Falls back to the registered default. */
  title?: ReactNode;
  /** Visual tone of the confirm action. Defaults to `'danger'`. */
  tone?: ConfirmTone;
  /**
   * Already-translated affirmative button label. Falls back to the registered
   * default.
   */
  confirmLabel?: ReactNode;
  /**
   * Already-translated dismissive button label. Falls back to the registered
   * default.
   */
  cancelLabel?: ReactNode;
}

/** Defaults supplied by the host app to avoid hardcoded English. */
export interface ConfirmActionDefaults {
  /** Default dialog title (e.g. "Are you sure?"). */
  title: ReactNode;
  /** Default confirm button label (e.g. "Confirm"). */
  confirmLabel: ReactNode;
  /** Default cancel button label (e.g. "Cancel"). */
  cancelLabel: ReactNode;
}

let resolveDefaults: () => ConfirmActionDefaults = () => ({
  title: 'Are you sure?',
  confirmLabel: 'Confirm',
  cancelLabel: 'Cancel',
});

/**
 * Register a per-call label resolver. The resolver is invoked each time
 * `confirmAction` runs so that language switches take effect immediately
 * without requiring the host app to re-register defaults.
 */
export function setConfirmActionLabels(provider: () => ConfirmActionDefaults): void {
  resolveDefaults = provider;
}

/** Show a themed confirm dialog and resolve to the user's choice. */
export function confirmAction(options: ConfirmActionOptions): Promise<boolean> {
  const defaults = resolveDefaults();
  return confirm.ask({
    title: options.title ?? defaults.title,
    message: options.message,
    confirmLabel: options.confirmLabel ?? defaults.confirmLabel,
    cancelLabel: options.cancelLabel ?? defaults.cancelLabel,
    tone: options.tone ?? 'danger',
  });
}
