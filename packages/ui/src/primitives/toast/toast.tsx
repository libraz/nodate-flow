/**
 * Toast — imperative notification system.
 *
 * Usage: render `<ToastProvider />` once near the root, then call
 * `toaster.show({ message: '...' })` from anywhere. Toasts are announced via a
 * polite ARIA live region (mirrors `src/a11y/live-region.tsx`), auto-dismiss
 * after `duration` ms, and pause their countdown while hovered or focused.
 */

import {
  type ReactElement,
  type ReactNode,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { createPortal } from 'react-dom';
import { cx } from '../../lib/cx';
import styles from './toast.module.css';

const PORTAL_ID = 'nf-toast-root';

export type ToastTone = 'info' | 'success' | 'warning' | 'danger';

export interface ToastOptions {
  /** Already-translated toast body. */
  message: ReactNode;
  /** Visual + semantic tone. Defaults to `'info'`. */
  tone?: ToastTone;
  /** Auto-dismiss duration in ms. Defaults to 5000. */
  duration?: number;
}

export interface Toast extends Required<Pick<ToastOptions, 'message'>> {
  id: string;
  tone: ToastTone;
  duration: number;
}

type Listener = (toasts: Toast[]) => void;

class ToastStore {
  private toasts: Toast[] = [];
  private listeners = new Set<Listener>();
  private counter = 0;

  getSnapshot = (): Toast[] => this.toasts;

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  private emit(): void {
    for (const l of this.listeners) l(this.toasts);
  }

  show(options: ToastOptions): string {
    this.counter += 1;
    const id = `nf-toast-${this.counter}`;
    const toast: Toast = {
      id,
      message: options.message,
      tone: options.tone ?? 'info',
      duration: options.duration ?? 5000,
    };
    this.toasts = [...this.toasts, toast];
    this.emit();
    return id;
  }

  dismiss(id: string): void {
    this.toasts = this.toasts.filter((t) => t.id !== id);
    this.emit();
  }

  clear(): void {
    this.toasts = [];
    this.emit();
  }
}

const store = new ToastStore();

/**
 * Imperative toast API. Call `toaster.show({ message: '...' })` from anywhere
 * (a `ToastProvider` must be mounted somewhere in the tree).
 */
export const toaster = {
  show: (options: ToastOptions): string => store.show(options),
  dismiss: (id: string): void => store.dismiss(id),
  clear: (): void => store.clear(),
};

/** Hook returning the live list of active toasts. */
export function useToaster(): Toast[] {
  return useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
}

function getPortalRoot(): HTMLElement | null {
  if (typeof document === 'undefined') return null;
  let el = document.getElementById(PORTAL_ID);
  if (!el) {
    el = document.createElement('div');
    el.id = PORTAL_ID;
    document.body.appendChild(el);
  }
  return el;
}

interface ToastItemProps {
  toast: Toast;
}

function ToastItem({ toast }: ToastItemProps): ReactElement {
  const [paused, setPaused] = useState(false);
  const remainingRef = useRef<number>(toast.duration);
  const startedRef = useRef<number>(Date.now());

  useEffect(() => {
    if (paused) {
      remainingRef.current -= Date.now() - startedRef.current;
      return;
    }
    startedRef.current = Date.now();
    const remaining = Math.max(0, remainingRef.current);
    const handle = window.setTimeout(() => {
      store.dismiss(toast.id);
    }, remaining);
    return () => window.clearTimeout(handle);
  }, [paused, toast.id]);

  const toneClass =
    toast.tone === 'success'
      ? styles.success
      : toast.tone === 'warning'
        ? styles.warning
        : toast.tone === 'danger'
          ? styles.danger
          : styles.info;

  return (
    <div
      className={cx(styles.toast, toneClass)}
      data-tone={toast.tone}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      onFocus={() => setPaused(true)}
      onBlur={() => setPaused(false)}
    >
      {toast.message}
    </div>
  );
}

export interface ToastProviderProps {
  /** Already-translated label for the live region. */
  label?: string;
}

/**
 * ToastProvider — mounts the toast viewport + ARIA live region. Render once
 * near the application root.
 */
export function ToastProvider({ label }: ToastProviderProps = {}): ReactElement | null {
  const toasts = useToaster();
  const root = getPortalRoot();
  if (!root) return null;

  return createPortal(
    <output aria-live="polite" aria-atomic="false" aria-label={label} className={styles.viewport}>
      {toasts.map((t) => (
        <ToastItem key={t.id} toast={t} />
      ))}
    </output>,
    root,
  );
}

export default ToastProvider;
