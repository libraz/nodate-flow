/**
 * Dialog — modal overlay with focus trap and portal.
 *
 * Renders into a deterministic portal container (`#nf-portal-root`, created on
 * demand). Reuses {@link useFocusTrap} for focus containment, restores focus to
 * the previously focused element on close, dismisses on Escape, and applies
 * `aria-modal` + `role="dialog"`. Background siblings get `inert` while open.
 *
 * Note: we deliberately do NOT use the native `<dialog>` element because
 * happy-dom's `showModal()` implementation is incomplete; a div + a11y
 * attributes gives us deterministic test behavior.
 */

import {
  type HTMLAttributes,
  type ReactElement,
  type ReactNode,
  type Ref,
  forwardRef,
  useCallback,
  useEffect,
  useId,
  useRef,
} from 'react';
import { createPortal } from 'react-dom';
import { useFocusTrap } from '../../hooks/use-focus-trap';
import { cx } from '../../lib/cx';
import styles from './dialog.module.css';

const PORTAL_ID = 'nf-portal-root';

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

export interface DialogProps extends Omit<HTMLAttributes<HTMLDivElement>, 'title'> {
  /** Whether the dialog is open. */
  open: boolean;
  /** Called when the dialog requests to close (escape, overlay click). */
  onClose: () => void;
  /** Already-translated dialog title. Wired to `aria-labelledby`. */
  title: ReactNode;
  /** Dialog body content. */
  children: ReactNode;
  /** When true, clicking the overlay closes the dialog. Defaults to `true`. */
  dismissOnOverlayClick?: boolean;
}

function DialogImpl(
  { open, onClose, title, children, className, dismissOnOverlayClick = true, ...rest }: DialogProps,
  ref: Ref<HTMLDivElement>,
): ReactElement | null {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const titleId = useId();

  useFocusTrap(containerRef, open);

  const handleRef = useCallback(
    (node: HTMLDivElement | null) => {
      containerRef.current = node;
      if (typeof ref === 'function') ref(node);
      else if (ref) ref.current = node;
    },
    [ref],
  );

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') {
        event.stopPropagation();
        onClose();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [open, onClose]);

  if (!open) return null;
  const root = getPortalRoot();
  if (!root) return null;

  return createPortal(
    // biome-ignore lint/a11y/useKeyWithClickEvents: overlay dismissal; keyboard handled by document keydown Escape
    // biome-ignore lint/a11y/useSemanticElements: native <dialog> is not used; div+role is intentional for happy-dom compat
    <div className={styles.overlay} onClick={dismissOnOverlayClick ? onClose : undefined}>
      <div
        ref={handleRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className={cx(styles.dialog, className)}
        onClick={(e) => e.stopPropagation()}
        {...rest}
      >
        <h2 id={titleId} className={styles.title}>
          {title}
        </h2>
        <div className={styles.body}>{children}</div>
      </div>
    </div>,
    root,
  );
}

const Dialog = forwardRef<HTMLDivElement, DialogProps>(DialogImpl);
Dialog.displayName = 'Dialog';

export default Dialog;
