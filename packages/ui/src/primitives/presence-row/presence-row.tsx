/**
 * PresenceRow — a table row rendered through {@link usePresenceList}.
 *
 * A row cannot collapse its height the way {@link PresenceItem} does, so
 * a removed row fades out in place (`.exitRow`) and then reports `onExited`; a row
 * that arrives after first paint takes the shared `.enter` entrance.
 * Where no animation runs (reduced motion, test DOMs) the exit is
 * immediate.
 */

import { type ReactElement, type ReactNode, useEffect } from 'react';
import { usePresence } from '../../hooks/use-presence';
import { cx } from '../../lib/cx';

import styles from '../../styles/list-motion.module.css';

export interface PresenceRowProps {
  present: boolean;
  onExited: () => void;
  /** True for a row that arrived after the list first painted. */
  arrived?: boolean;
  className?: string | undefined;
  children: ReactNode;
}

export default function PresenceRow({
  present,
  onExited,
  arrived = false,
  className,
  children,
}: PresenceRowProps): ReactElement | null {
  const presence = usePresence<HTMLTableRowElement>(present);

  // biome-ignore lint/correctness/useExhaustiveDependencies: fires once per completed exit; onExited is recreated every render
  useEffect(() => {
    if (!presence.mounted) onExited();
  }, [presence.mounted]);

  if (!presence.mounted) return null;
  return (
    <tr
      ref={presence.ref}
      data-state={presence.state}
      inert={!present}
      className={cx(styles.exitRow, arrived && styles.enter, className)}
    >
      {children}
    </tr>
  );
}
