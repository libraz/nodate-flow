/**
 * Popover — click-triggered floating panel with focus trap.
 *
 * Backed by floating-ui for positioning. Click the trigger (or activate via
 * keyboard) to open; Escape and outside click dismiss; focus is trapped inside
 * while open and restored to the trigger on close.
 */

import {
  autoUpdate,
  FloatingFocusManager,
  FloatingPortal,
  flip,
  offset,
  type Placement,
  shift,
  useClick,
  useDismiss,
  useFloating,
  useInteractions,
  useRole,
} from '@floating-ui/react';
import {
  type CSSProperties,
  cloneElement,
  isValidElement,
  type ReactElement,
  type ReactNode,
  useState,
} from 'react';
import { cx } from '../../lib/cx';
import styles from './popover.module.css';

export interface PopoverProps {
  /** Trigger element. Receives ref + click handler. */
  children: ReactElement;
  /** Popover body content. */
  content: ReactNode;
  /** Floating placement. Defaults to `'bottom-start'`. */
  placement?: Placement;
  /** Controlled open state. */
  open?: boolean;
  /** Called when the open state changes. */
  onOpenChange?: (open: boolean) => void;
  /** Optional className for the popover panel. */
  className?: string;
  /**
   * Lock the panel to a fixed minimum block-size. Use for kind/mode-swap
   * popovers whose inner content grows or shrinks: without a floor the panel
   * reflows on every switch, which contradicts the "morphic UI must not
   * reflow" rule. Accepts any CSS length (`'18rem'`, `'240px'`, ...).
   */
  minBlockSize?: string;
  /**
   * Lock the panel to a fixed minimum inline-size. Default of `12rem` is set
   * by the CSS module; pass to override (e.g. wider kind picker).
   */
  minInlineSize?: string;
}

/**
 * Popover primitive. See {@link PopoverProps}.
 */
export default function Popover({
  children,
  content,
  placement = 'bottom-start',
  open: controlledOpen,
  onOpenChange,
  className,
  minBlockSize,
  minInlineSize,
}: PopoverProps): ReactElement {
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false);
  const isControlled = controlledOpen !== undefined;
  const open = isControlled ? controlledOpen : uncontrolledOpen;
  const setOpen = (next: boolean): void => {
    if (!isControlled) setUncontrolledOpen(next);
    onOpenChange?.(next);
  };

  const { refs, floatingStyles, context } = useFloating({
    open,
    onOpenChange: setOpen,
    placement,
    middleware: [offset(8), flip(), shift({ padding: 8 })],
    whileElementsMounted: autoUpdate,
  });

  const click = useClick(context);
  const dismiss = useDismiss(context, { escapeKey: true, outsidePress: true });
  const role = useRole(context, { role: 'dialog' });

  const { getReferenceProps, getFloatingProps } = useInteractions([click, dismiss, role]);

  if (!isValidElement(children)) {
    return children;
  }

  const trigger = cloneElement(
    children,
    getReferenceProps({ ref: refs.setReference, ...(children.props as object) }),
  );

  const lockStyle: CSSProperties =
    minBlockSize !== undefined || minInlineSize !== undefined
      ? {
          ...floatingStyles,
          ...(minBlockSize !== undefined ? { minBlockSize } : {}),
          ...(minInlineSize !== undefined ? { minInlineSize } : {}),
        }
      : floatingStyles;

  return (
    <>
      {trigger}
      {open ? (
        <FloatingPortal>
          <FloatingFocusManager context={context} modal={false}>
            <div
              ref={refs.setFloating}
              style={lockStyle}
              className={cx(styles.popover, className)}
              {...getFloatingProps()}
            >
              {content}
            </div>
          </FloatingFocusManager>
        </FloatingPortal>
      ) : null}
    </>
  );
}
