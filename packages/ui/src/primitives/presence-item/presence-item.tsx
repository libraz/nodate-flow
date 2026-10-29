/**
 * PresenceItem — one entry of a {@link usePresenceList} list.
 *
 * While `present` it renders its children inside a collapsible wrapper;
 * once `present` turns false the wrapper collapses and fades, then
 * reports `onExited` so the list drops the entry. Where no animation
 * runs (reduced motion, test DOMs) the exit is immediate.
 */

import { type CSSProperties, type ReactElement, type ReactNode, useEffect } from 'react';
import { usePresence } from '../../hooks/use-presence';
import { cx } from '../../lib/cx';

import styles from '../../styles/list-motion.module.css';

export interface PresenceItemProps {
  present: boolean;
  onExited: () => void;
  /** Element to render; `li` inside a list, `div` elsewhere. */
  as?: 'li' | 'div';
  role?: string | undefined;
  className?: string | undefined;
  style?: CSSProperties | undefined;
  children: ReactNode;
}

export default function PresenceItem({
  present,
  onExited,
  as: Tag = 'div',
  role,
  className,
  style,
  children,
}: PresenceItemProps): ReactElement | null {
  const presence = usePresence<HTMLElement>(present);

  // biome-ignore lint/correctness/useExhaustiveDependencies: fires once per completed exit; onExited is recreated every render
  useEffect(() => {
    if (!presence.mounted) onExited();
  }, [presence.mounted]);

  if (!presence.mounted) return null;
  return (
    <Tag
      ref={presence.ref}
      data-state={presence.state}
      inert={!present}
      role={role}
      className={cx(styles.presence, className)}
      style={style}
    >
      <div>{children}</div>
    </Tag>
  );
}
