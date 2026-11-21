/**
 * AuthStep — one step of a multi-step auth card.
 *
 * Steps swap in sequence: the leaving step collapses (so only one copy of
 * a shared field is ever in the DOM), `onExited` fires once it is gone,
 * and the caller then opens the next. Height eases through `Collapse`.
 * `direction` slides the arriving step in along the reading direction;
 * omit it for the step a page opens on.
 */

import Collapse from '@nodate-flow/ui/primitives/collapse';
import { type ReactElement, type ReactNode, useEffect, useRef, useState } from 'react';

export type AuthStepDirection = 'forward' | 'back';

export interface AuthStepProps {
  open: boolean;
  /** Which way the step arrives; unset means it does not move. */
  direction?: AuthStepDirection | undefined;
  /** Called after the closing step has left the DOM; a single-step page can omit it. */
  onExited?: (() => void) | undefined;
  children: ReactNode;
}

const STEP_CLASS: Record<AuthStepDirection, string> = {
  forward: 'aw-step-forward',
  back: 'aw-step-back',
};

/** Collapse unmounts its children after the exit; the cleanup is the signal. */
function ExitProbe({
  onExited,
  direction,
  children,
}: Pick<AuthStepProps, 'onExited' | 'direction' | 'children'>) {
  // Read once on arrival: a later change must not replay the slide on a step that is leaving.
  const [arrival] = useState(direction);
  const latest = useRef(onExited);
  useEffect(() => {
    latest.current = onExited;
  });
  useEffect(
    () => () => {
      latest.current?.();
    },
    [],
  );
  return <div className={arrival ? STEP_CLASS[arrival] : undefined}>{children}</div>;
}

export default function AuthStep({
  open,
  direction,
  onExited,
  children,
}: AuthStepProps): ReactElement {
  return (
    <Collapse open={open}>
      <ExitProbe onExited={onExited} direction={direction}>
        {children}
      </ExitProbe>
    </Collapse>
  );
}
