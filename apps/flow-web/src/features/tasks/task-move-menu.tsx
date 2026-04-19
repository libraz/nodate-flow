/**
 * TaskMoveMenu — accessible menu that lists legal state transitions for a
 * task card on the board view.
 *
 * This provides a keyboard-accessible alternative to the HTML5 drag-and-drop
 * interaction. The trigger button opens a popover with one item per legal
 * transition; selecting an item fires the same `useTransitionTask` mutation
 * that D&D uses.
 */

import Popover from '@nodate-flow/ui/primitives/popover';
import { MoreVertical } from 'lucide-react';
import { type ReactElement, useCallback, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { TRANSITIONS_BY_STATE, type TaskDerivedState, type TransitionName } from './api';
import { STATE_KEY } from './constants';

/** Maps a transition verb to the state the task lands in. */
const LANDING_STATE: Record<TransitionName, TaskDerivedState> = {
  start: 'waiting',
  block: 'open',
  unblock: 'waiting',
  submit: 'review',
  complete: 'done',
  reopen: 'open',
  cancel: 'cancelled',
};

export interface TaskMoveMenuProps {
  /** Current derived state of the task. */
  state: TaskDerivedState;
  /** Called when the user picks a transition. */
  onTransition: (transition: TransitionName, landingState: TaskDerivedState) => void;
}

export default function TaskMoveMenu({ state, onTransition }: TaskMoveMenuProps): ReactElement {
  const { t } = useTranslation('common');
  const [open, setOpen] = useState(false);
  const itemsRef = useRef<(HTMLButtonElement | null)[]>([]);
  const legal = TRANSITIONS_BY_STATE[state] ?? [];

  const handleSelect = useCallback(
    (transition: TransitionName) => {
      setOpen(false);
      onTransition(transition, LANDING_STATE[transition]);
    },
    [onTransition],
  );

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent, idx: number) => {
      switch (e.key) {
        case 'ArrowDown': {
          e.preventDefault();
          const next = (idx + 1) % legal.length;
          itemsRef.current[next]?.focus();
          break;
        }
        case 'ArrowUp': {
          e.preventDefault();
          const prev = (idx - 1 + legal.length) % legal.length;
          itemsRef.current[prev]?.focus();
          break;
        }
        case 'Home':
          e.preventDefault();
          itemsRef.current[0]?.focus();
          break;
        case 'End':
          e.preventDefault();
          itemsRef.current[legal.length - 1]?.focus();
          break;
      }
    },
    [legal.length],
  );

  if (legal.length === 0) return <></>;

  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      placement="bottom-end"
      content={
        <div
          role="menu"
          aria-label={t('tasks.board.move_menu')}
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: '0.125rem',
            margin: 'calc(-1 * var(--nf-space-4, 1rem))',
            padding: 'var(--nf-space-2, 0.5rem)',
          }}
        >
          {legal.map((name, idx) => {
            const landing = LANDING_STATE[name];
            return (
              <button
                key={name}
                ref={(el) => {
                  itemsRef.current[idx] = el;
                }}
                role="menuitem"
                type="button"
                onClick={() => handleSelect(name)}
                onKeyDown={(e) => handleKeyDown(e, idx)}
                style={{
                  all: 'unset',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  gap: '0.75rem',
                  padding: '0.375rem 0.5rem',
                  borderRadius: 'var(--nf-radius-sm, 0.25rem)',
                  cursor: 'pointer',
                  fontSize: 'var(--nf-text-sm, 0.875rem)',
                  lineHeight: 1.4,
                }}
                onMouseEnter={(e) => {
                  (e.currentTarget as HTMLElement).style.background =
                    'var(--nf-color-bg-hover, var(--color-surface-raised))';
                }}
                onMouseLeave={(e) => {
                  (e.currentTarget as HTMLElement).style.background = 'transparent';
                }}
                onFocus={(e) => {
                  (e.currentTarget as HTMLElement).style.background =
                    'var(--nf-color-bg-hover, var(--color-surface-raised))';
                }}
                onBlur={(e) => {
                  (e.currentTarget as HTMLElement).style.background = 'transparent';
                }}
              >
                <span>{t(`tasks.transitions.${name}`)}</span>
                <span
                  style={{
                    color: 'var(--nf-color-fg-muted, var(--color-muted))',
                    fontSize: 'var(--nf-text-xs, 0.75rem)',
                  }}
                >
                  → {t(STATE_KEY[landing])}
                </span>
              </button>
            );
          })}
        </div>
      }
    >
      <button
        type="button"
        aria-label={t('tasks.board.move_menu')}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={(e) => {
          // Prevent the card's onClick from navigating to task detail.
          e.stopPropagation();
        }}
        style={{
          all: 'unset',
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: '1.5rem',
          height: '1.5rem',
          borderRadius: 'var(--nf-radius-sm, 0.25rem)',
          color: 'var(--nf-color-fg-muted, var(--color-muted))',
          cursor: 'pointer',
          flexShrink: 0,
        }}
        onMouseEnter={(e) => {
          (e.currentTarget as HTMLElement).style.background =
            'var(--nf-color-bg-hover, var(--color-surface-raised))';
        }}
        onMouseLeave={(e) => {
          (e.currentTarget as HTMLElement).style.background = 'transparent';
        }}
        onFocus={(e) => {
          (e.currentTarget as HTMLElement).style.background =
            'var(--nf-color-bg-hover, var(--color-surface-raised))';
        }}
        onBlur={(e) => {
          (e.currentTarget as HTMLElement).style.background = 'transparent';
        }}
      >
        <MoreVertical size={14} aria-hidden />
      </button>
    </Popover>
  );
}
