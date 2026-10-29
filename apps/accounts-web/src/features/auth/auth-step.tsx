/**
 * AuthStep — one step of a multi-step auth card.
 *
 * Steps swap in sequence: the leaving step collapses (so only one copy of
 * a shared field is ever in the DOM), `onExited` fires once it is gone,
 * and the caller then opens the next. Height eases through `Collapse`.
 */

import Collapse from '@nodate-flow/ui/primitives/collapse';
import { type ReactElement, type ReactNode, useEffect, useRef } from 'react';

export interface AuthStepProps {
  open: boolean;
  /** Called after the closing step has left the DOM. */
  onExited: () => void;
  children: ReactNode;
}

/** Collapse unmounts its children after the exit; the cleanup is the signal. */
function ExitProbe({ onExited, children }: Pick<AuthStepProps, 'onExited' | 'children'>) {
  const latest = useRef(onExited);
  useEffect(() => {
    latest.current = onExited;
  });
  useEffect(
    () => () => {
      latest.current();
    },
    [],
  );
  return children;
}

export default function AuthStep({ open, onExited, children }: AuthStepProps): ReactElement {
  return (
    <Collapse open={open}>
      <ExitProbe onExited={onExited}>{children}</ExitProbe>
    </Collapse>
  );
}
