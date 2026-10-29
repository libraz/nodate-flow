/**
 * Collapse — the one sanctioned height animation, for disclosures,
 * expanders and tree children.
 *
 * Children stay mounted while the exit plays and unmount after it. The
 * wrapper is `inert` as soon as `open` is false, so closing content is
 * neither focusable nor exposed to assistive tech. Where no animation
 * runs (reduced motion, test DOMs) the unmount is immediate.
 */

import { type ReactElement, type ReactNode, useRef } from 'react';
import { usePresence } from '../../hooks/use-presence';
import { cx } from '../../lib/cx';
import styles from './collapse.module.css';

export interface CollapseProps {
  open: boolean;
  children: ReactNode;
  className?: string | undefined;
  /** Target for the trigger's `aria-controls`; present only while mounted. */
  id?: string | undefined;
}

export default function Collapse({
  open,
  children,
  className,
  id,
}: CollapseProps): ReactElement | null {
  const presence = usePresence<HTMLDivElement>(open);
  // Content open at first paint is not an arrival, so it skips the enter.
  const skipEnter = useRef(open);
  if (!open) skipEnter.current = false;

  if (!presence.mounted) return null;
  return (
    <div
      ref={presence.ref}
      id={id}
      data-state={presence.state}
      data-skip-enter={skipEnter.current ? '' : undefined}
      inert={!open}
      className={cx(styles.root, className)}
    >
      <div className={styles.inner}>{children}</div>
    </div>
  );
}
