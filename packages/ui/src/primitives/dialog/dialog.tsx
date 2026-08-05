/**
 * Dialog — modal overlay with focus trap and portal.
 *
 * Renders into a deterministic portal container (`#nf-portal-root`, created on
 * demand). Reuses {@link useFocusTrap} for focus containment, restores focus to
 * the previously focused element on close, dismisses on Escape, and applies
 * `aria-modal` + `role="dialog"`. While open, background `<body>` children
 * (everything except `#nf-portal-root`) are made `inert` and `aria-hidden`,
 * and body scroll is locked via {@link useOverlayLock}; both are
 * reference-counted so stacked overlays cooperate.
 *
 * Note: we deliberately do NOT use the native `<dialog>` element because
 * happy-dom's `showModal()` implementation is incomplete; a div + a11y
 * attributes gives us deterministic test behavior.
 */

import {
  forwardRef,
  type HTMLAttributes,
  type ReactElement,
  type ReactNode,
  type Ref,
  useCallback,
  useEffect,
  useId,
  useRef,
} from 'react';
import { createPortal } from 'react-dom';
import { useFocusTrap } from '../../hooks/use-focus-trap';
import { cx } from '../../lib/cx';
import { useOverlayLock } from '../_overlay/overlay-lock';
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
  /** When true, renders as bottom sheet on mobile (< 768px). Defaults to `false`. */
  fullScreenOnMobile?: boolean;
  /**
   * Maximum inline size of the dialog body. Mobile bottom-sheet variant
   * (< 768px) ignores this and uses 100% width regardless.
   * - 'sm': 24rem — confirms, very short prompts.
   * - 'md' (default): 32rem — fits short forms.
   * - 'lg': 36rem — content-heavy dialogs (unified pickers, multi-column).
   * - 'xl': 40rem — very dense dialogs (settings panels, data inspectors).
   */
  size?: 'sm' | 'md' | 'lg' | 'xl';
}

function sizeClass(size: 'sm' | 'md' | 'lg' | 'xl'): string | undefined {
  switch (size) {
    case 'sm':
      return styles.sizeSm;
    case 'lg':
      return styles.sizeLg;
    case 'xl':
      return styles.sizeXl;
    default:
      return styles.sizeMd;
  }
}

function DialogImpl(
  {
    open,
    onClose,
    title,
    children,
    className,
    dismissOnOverlayClick = true,
    fullScreenOnMobile = false,
    size = 'md',
    ...rest
  }: DialogProps,
  ref: Ref<HTMLDivElement>,
): ReactElement | null {
  const containerRef = useRef<HTMLDivElement | null>(null);
  /** Whether the pointer went down on the overlay itself. See the handlers below. */
  const pressedOnOverlayRef = useRef(false);
  const titleId = useId();

  useFocusTrap(containerRef, open);
  useOverlayLock(containerRef, open);

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
    // biome-ignore lint/a11y/noStaticElementInteractions: overlay dismissal; keyboard handled by document keydown Escape
    // biome-ignore lint/a11y/useKeyWithClickEvents: overlay dismissal; keyboard handled by document keydown Escape
    <div
      className={cx(styles.overlay, fullScreenOnMobile && styles.overlayMobile)}
      /*
       * Dismiss only when the gesture both started and ended on the
       * overlay. A `click` fires on the nearest common ancestor of
       * pointerdown and pointerup, so dragging a text selection out of a
       * field and releasing past the dialog's edge counted as an overlay
       * click — the dialog closed and everything typed into it went with
       * it. Selecting text is the ordinary way to edit, so this was
       * reachable without doing anything unusual.
       */
      onPointerDown={
        dismissOnOverlayClick
          ? (event) => {
              pressedOnOverlayRef.current = event.target === event.currentTarget;
            }
          : undefined
      }
      onClick={
        dismissOnOverlayClick
          ? (event) => {
              const startedHere = pressedOnOverlayRef.current;
              pressedOnOverlayRef.current = false;
              if (startedHere && event.target === event.currentTarget) onClose();
            }
          : undefined
      }
    >
      {/* biome-ignore lint/a11y/useKeyWithClickEvents: onClick only stops overlay-dismiss propagation; dialog keyboard handled at document level */}
      <div
        ref={handleRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        data-size={size}
        className={cx(
          styles.dialog,
          sizeClass(size),
          fullScreenOnMobile && styles.dialogMobile,
          className,
        )}
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
