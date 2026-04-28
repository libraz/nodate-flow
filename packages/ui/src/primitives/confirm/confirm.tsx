/**
 * Confirm — imperative themed confirmation dialog.
 *
 * Usage: render `<ConfirmProvider />` once near the root, then call
 * `confirm.ask({ title, message }): Promise<boolean>` from anywhere. Replaces
 * `window.confirm()` with a themed Dialog that honors design tokens.
 */

import { type ReactElement, type ReactNode, useSyncExternalStore } from 'react';
import Button from '../button/button';
import Dialog from '../dialog/dialog';

export type ConfirmTone = 'neutral' | 'danger';

export interface ConfirmOptions {
  /** Already-translated dialog title. */
  title: ReactNode;
  /** Already-translated body message. */
  message: ReactNode;
  /** Confirm button label (already translated). */
  confirmLabel?: ReactNode;
  /** Cancel button label (already translated). */
  cancelLabel?: ReactNode;
  /** Visual tone of the confirm action. Defaults to `'neutral'`. */
  tone?: ConfirmTone;
}

interface ConfirmState extends ConfirmOptions {
  id: number;
  resolve: (value: boolean) => void;
}

type Listener = (state: ConfirmState | null) => void;

class ConfirmStore {
  private current: ConfirmState | null = null;
  private listeners = new Set<Listener>();
  private counter = 0;

  getSnapshot = (): ConfirmState | null => this.current;

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  private emit(): void {
    for (const l of this.listeners) l(this.current);
  }

  ask(options: ConfirmOptions): Promise<boolean> {
    // Reject any in-flight confirmation to avoid orphaned promises.
    if (this.current) {
      this.current.resolve(false);
    }
    this.counter += 1;
    return new Promise<boolean>((resolve) => {
      this.current = { ...options, id: this.counter, resolve };
      this.emit();
    });
  }

  resolve(value: boolean): void {
    if (!this.current) return;
    const { resolve } = this.current;
    this.current = null;
    this.emit();
    resolve(value);
  }
}

const store = new ConfirmStore();

/**
 * Imperative confirm API. Call `confirm.ask({ title, message })` and await the
 * returned boolean. A `ConfirmProvider` must be mounted somewhere in the tree.
 */
export const confirm = {
  ask: (options: ConfirmOptions): Promise<boolean> => store.ask(options),
};

/** Hook returning the current pending confirmation, if any. */
export function useConfirm(): ConfirmState | null {
  return useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
}

/**
 * ConfirmProvider — mounts the confirmation dialog host. Render once near the
 * application root.
 */
export function ConfirmProvider(): ReactElement {
  const state = useConfirm();
  const open = state !== null;

  const handleCancel = (): void => {
    store.resolve(false);
  };
  const handleConfirm = (): void => {
    store.resolve(true);
  };

  return (
    <Dialog open={open} onClose={handleCancel} title={state?.title ?? ''}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
        <div style={{ whiteSpace: 'pre-wrap' }}>{state?.message}</div>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem' }}>
          <Button
            type="button"
            variant="ghost"
            data-testid="confirm-dialog-cancel"
            onClick={handleCancel}
          >
            {state?.cancelLabel ?? 'Cancel'}
          </Button>
          <Button
            type="button"
            variant={state?.tone === 'danger' ? 'danger' : 'primary'}
            data-testid="confirm-dialog-confirm"
            onClick={handleConfirm}
            autoFocus
          >
            {state?.confirmLabel ?? 'OK'}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

export default ConfirmProvider;
