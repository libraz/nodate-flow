import { type Dispatch, type ReactElement, type SetStateAction, useState } from 'react';
import css from './task-workflow-panel.module.css';

/** The control an action error belongs to; the message renders beside it. */
export type ActionScope =
  | 'policy'
  | 'checks'
  | 'submit'
  | 'reason'
  | 'decision'
  | 'handoff'
  | 'handoffDecision';

interface ActionErrorState {
  scope: ActionScope;
  message: string;
  targetId?: string;
}

export interface ActionErrorSlot {
  actionError: ActionErrorState | null;
  setActionError: Dispatch<SetStateAction<ActionErrorState | null>>;
  failWith: (scope: ActionScope, message: string, targetId?: string) => void;
  errorFor: (scope: ActionScope, targetId?: string) => ReactElement | null;
}

/** Holds the single scoped action error and the helpers that set and render it. */
export function useActionError(): ActionErrorSlot {
  // `targetId` pins a row-level error (one handoff) to that row.
  const [actionError, setActionError] = useState<{
    scope: ActionScope;
    message: string;
    targetId?: string;
  } | null>(null);
  const failWith = (scope: ActionScope, message: string, targetId?: string): void => {
    setActionError({ scope, message, ...(targetId ? { targetId } : {}) });
  };
  const errorFor = (scope: ActionScope, targetId?: string): ReactElement | null =>
    actionError?.scope === scope && actionError.targetId === targetId ? (
      <p className={targetId ? `${css.error} ${css.rowError}` : css.error} role="alert">
        {actionError.message}
      </p>
    ) : null;
  return { actionError, setActionError, failWith, errorFor };
}
