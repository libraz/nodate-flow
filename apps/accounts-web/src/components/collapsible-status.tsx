/**
 * CollapsibleStatus — a message that sits between a form's last field and
 * its submit button. It eases open so the button it displaces moves with
 * the height instead of jumping, and keeps the last message on screen
 * while it eases shut.
 */

import Collapse from '@nodate-flow/ui/primitives/collapse';
import { type ReactElement, type ReactNode, useState } from 'react';

import styles from './collapsible-status.module.css';

export interface CollapsibleStatusProps<T> {
  /** The message to show; `null` closes the slot. */
  value: T | null;
  /** Renders the message element (carrying its own role / live semantics). */
  children: (value: T) => ReactNode;
}

export default function CollapsibleStatus<T>({
  value,
  children,
}: CollapsibleStatusProps<T>): ReactElement {
  const [latched, setLatched] = useState<T | null>(value);
  if (value !== null && value !== latched) setLatched(value);
  return (
    <Collapse open={value !== null} className={styles.slot}>
      <div className={styles.body}>{latched !== null ? children(latched) : null}</div>
    </Collapse>
  );
}
