/**
 * Tooltip — accessible hover/focus tooltip backed by floating-ui.
 *
 * Wrap any focusable trigger in `<Tooltip content={...}>`. Opens on hover or
 * focus after a short delay, dismisses on Escape, blur, or pointer leave.
 * Renders into a {@link FloatingPortal} with `role="tooltip"`.
 */

import {
  FloatingPortal,
  type Placement,
  autoUpdate,
  flip,
  offset,
  shift,
  useDismiss,
  useFloating,
  useFocus,
  useHover,
  useInteractions,
  useRole,
} from '@floating-ui/react';
import { type ReactElement, type ReactNode, cloneElement, isValidElement, useState } from 'react';
import { cx } from '../../lib/cx';
import styles from './tooltip.module.css';

export interface TooltipProps {
  /** Already-translated tooltip content. */
  content: ReactNode;
  /** Trigger element. Must be a single React element accepting refs / event props. */
  children: ReactElement;
  /** Floating-UI placement. Defaults to `'top'`. */
  placement?: Placement;
  /** Open delay in ms. Defaults to 200. */
  delay?: number;
  /** Optional className applied to the tooltip body. */
  className?: string;
}

/**
 * Tooltip primitive. See {@link TooltipProps}.
 */
export default function Tooltip({
  content,
  children,
  placement = 'top',
  delay = 200,
  className,
}: TooltipProps): ReactElement {
  const [open, setOpen] = useState(false);
  const { refs, floatingStyles, context } = useFloating({
    open,
    onOpenChange: setOpen,
    placement,
    middleware: [offset(6), flip(), shift({ padding: 8 })],
    whileElementsMounted: autoUpdate,
  });

  const hover = useHover(context, { delay: { open: delay, close: 0 }, move: false });
  const focus = useFocus(context);
  const dismiss = useDismiss(context, { escapeKey: true });
  const role = useRole(context, { role: 'tooltip' });

  const { getReferenceProps, getFloatingProps } = useInteractions([hover, focus, dismiss, role]);

  if (!isValidElement(children)) {
    return children;
  }

  const trigger = cloneElement(
    children,
    getReferenceProps({ ref: refs.setReference, ...(children.props as object) }),
  );

  return (
    <>
      {trigger}
      {open ? (
        <FloatingPortal>
          <div
            ref={refs.setFloating}
            style={floatingStyles}
            className={cx(styles.tooltip, className)}
            {...getFloatingProps()}
          >
            {content}
          </div>
        </FloatingPortal>
      ) : null}
    </>
  );
}
