/**
 * Drawer — side-sheet variant of Dialog.
 *
 * Same a11y guarantees as Dialog (focus trap, escape, role="dialog" +
 * aria-modal, portal). Slides in from the configured side: `inline-start`,
 * `inline-end`, `block-start`, or `block-end` (logical sides only).
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
import styles from './drawer.module.css';

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

export type DrawerSide = 'inline-start' | 'inline-end' | 'block-start' | 'block-end';

export interface DrawerProps extends Omit<HTMLAttributes<HTMLDivElement>, 'title'> {
  open: boolean;
  onClose: () => void;
  /** Already-translated drawer title. */
  title: ReactNode;
  children: ReactNode;
  /** Logical side. Defaults to `'inline-end'`. */
  side?: DrawerSide;
  dismissOnOverlayClick?: boolean;
}

function DrawerImpl(
  {
    open,
    onClose,
    title,
    children,
    side = 'inline-end',
    className,
    dismissOnOverlayClick = true,
    ...rest
  }: DrawerProps,
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

  const sideClass =
    side === 'inline-start'
      ? styles.inlineStart
      : side === 'block-start'
        ? styles.blockStart
        : side === 'block-end'
          ? styles.blockEnd
          : styles.inlineEnd;

  return createPortal(
    // biome-ignore lint/a11y/useKeyWithClickEvents: overlay dismissal; keyboard handled by document keydown Escape
    // biome-ignore lint/a11y/useSemanticElements: div+role=dialog pattern is intentional (no native dialog element)
    <div className={styles.overlay} onClick={dismissOnOverlayClick ? onClose : undefined}>
      <div
        ref={handleRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        data-side={side}
        className={cx(styles.drawer, sideClass, className)}
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

const Drawer = forwardRef<HTMLDivElement, DrawerProps>(DrawerImpl);
Drawer.displayName = 'Drawer';

export default Drawer;
