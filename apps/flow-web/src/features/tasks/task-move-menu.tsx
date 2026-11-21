/**
 * TaskMoveMenu — accessible menu that lists legal state transitions for a
 * task card on the board view, plus archive/unarchive actions.
 *
 * This provides a keyboard-accessible alternative to the HTML5 drag-and-drop
 * interaction. The trigger button opens a popover with one item per legal
 * transition; selecting an item fires the same `useTransitionTask` mutation
 * that D&D uses.
 */

import Popover from '@nodate-flow/ui/primitives/popover';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Archive, ArchiveRestore, MoreVertical } from 'lucide-react';
import { type ReactElement, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import {
  landingStateFor,
  type TaskDerivedState,
  TRANSITIONS_BY_STATE,
  type TransitionName,
  useArchiveTask,
  useUnarchiveTask,
} from './api';
import { STATE_KEY } from './constants';
import StateDot from './state-dot';
import styles from './task-move-menu.module.css';

const TRANSITION_LABEL_KEY: Record<TransitionName, string> = {
  start: 'tasks.detail.transitions.start',
  block: 'tasks.detail.transitions.block',
  unblock: 'tasks.detail.transitions.unblock',
  submit: 'tasks.detail.transitions.submit',
  complete: 'tasks.detail.transitions.complete',
  reopen: 'tasks.detail.transitions.reopen',
  cancel: 'tasks.detail.transitions.cancel',
};

export interface TaskMoveMenuProps {
  /** Current derived state of the task. */
  state: TaskDerivedState;
  /** Called when the user picks a transition. */
  onTransition: (transition: TransitionName, landingState: TaskDerivedState) => void;
  /** Public ID of the task — required for archive/unarchive. */
  taskId?: string;
  /** Unix timestamp (seconds) when the task was archived, or undefined/null. */
  archivedAt?: number | null;
}

export default function TaskMoveMenu({
  state,
  onTransition,
  taskId,
  archivedAt,
}: TaskMoveMenuProps): ReactElement | null {
  const { t } = useTranslation(['common', 'labels']);
  const [open, setOpen] = useState(false);
  const itemsRef = useRef<(HTMLButtonElement | null)[]>([]);
  // Each legal verb paired with where the server will actually put the task.
  const legal = (TRANSITIONS_BY_STATE[state] ?? []).flatMap((name) => {
    const landing = landingStateFor(state, name);
    return landing ? [{ name, landing }] : [];
  });

  const archiveMutation = useArchiveTask();
  const unarchiveMutation = useUnarchiveTask();
  const isArchived = archivedAt != null && archivedAt > 0;

  const handleSelect = (transition: TransitionName, landing: TaskDerivedState) => {
    setOpen(false);
    onTransition(transition, landing);
  };

  // mutateAsync: the menu (and its card) may be gone by the time Undo is pressed.
  const undoWith = (reverse: typeof archiveMutation, errorKey: string): void => {
    if (!taskId) return;
    reverse.mutateAsync(taskId).catch((err: unknown) => {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, errorKey) });
    });
  };

  const handleArchive = () => {
    if (!taskId) return;
    setOpen(false);

    if (isArchived) {
      unarchiveMutation.mutate(taskId, {
        onSuccess: () => {
          toaster.show({
            tone: 'info',
            message: t('archive.unarchived', { ns: 'labels' }),
            action: {
              label: t('common.undo'),
              onAction: () => undoWith(archiveMutation, 'labels:archive.error_archive'),
            },
          });
        },
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'labels:archive.error_unarchive'),
          });
        },
      });
    } else {
      archiveMutation.mutate(taskId, {
        onSuccess: () => {
          toaster.show({
            tone: 'info',
            message: t('archive.archived', { ns: 'labels' }),
            action: {
              label: t('common.undo'),
              onAction: () => undoWith(unarchiveMutation, 'labels:archive.error_unarchive'),
            },
          });
        },
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'labels:archive.error_archive'),
          });
        },
      });
    }
  };

  const totalItems = legal.length + (taskId ? 1 : 0);

  const handleKeyDown = (e: React.KeyboardEvent, idx: number) => {
    switch (e.key) {
      case 'ArrowDown': {
        e.preventDefault();
        const next = (idx + 1) % totalItems;
        itemsRef.current[next]?.focus();
        break;
      }
      case 'ArrowUp': {
        e.preventDefault();
        const prev = (idx - 1 + totalItems) % totalItems;
        itemsRef.current[prev]?.focus();
        break;
      }
      case 'Home':
        e.preventDefault();
        itemsRef.current[0]?.focus();
        break;
      case 'End':
        e.preventDefault();
        itemsRef.current[totalItems - 1]?.focus();
        break;
    }
  };

  if (totalItems === 0) return null;

  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      placement="bottom-end"
      content={
        <div role="menu" aria-label={t('tasks.board.move_menu')} className={styles.menuList}>
          {legal.map(({ name, landing }, idx) => {
            return (
              <button
                key={name}
                ref={(el) => {
                  itemsRef.current[idx] = el;
                }}
                role="menuitem"
                type="button"
                className={styles.menuItem}
                onClick={() => handleSelect(name, landing)}
                onKeyDown={(e) => handleKeyDown(e, idx)}
              >
                <span>{t(TRANSITION_LABEL_KEY[name])}</span>
                <span className={styles.menuItemLanding}>
                  <span aria-hidden="true">→</span>
                  <StateDot state={landing} />
                  {t(STATE_KEY[landing])}
                </span>
              </button>
            );
          })}
          {taskId ? (
            <>
              {legal.length > 0 ? <hr className={styles.menuDivider} /> : null}
              <button
                key="archive"
                ref={(el) => {
                  itemsRef.current[legal.length] = el;
                }}
                role="menuitem"
                type="button"
                className={styles.menuItem}
                onClick={handleArchive}
                onKeyDown={(e) => handleKeyDown(e, legal.length)}
              >
                {isArchived ? (
                  <>
                    <ArchiveRestore size={14} aria-hidden />
                    <span>{t('archive.unarchive', { ns: 'labels' })}</span>
                  </>
                ) : (
                  <>
                    <Archive size={14} aria-hidden />
                    <span>{t('archive.action', { ns: 'labels' })}</span>
                  </>
                )}
              </button>
            </>
          ) : null}
        </div>
      }
    >
      <button
        type="button"
        aria-label={t('tasks.board.move_menu')}
        aria-haspopup="menu"
        aria-expanded={open}
        className={styles.trigger}
        onClick={(e) => {
          // Prevent the card's onClick from navigating to task detail.
          e.stopPropagation();
        }}
      >
        <MoreVertical size={14} aria-hidden />
      </button>
    </Popover>
  );
}
