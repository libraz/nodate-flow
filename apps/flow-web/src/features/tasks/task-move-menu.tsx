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
  type TaskDerivedState,
  TRANSITIONS_BY_STATE,
  type TransitionName,
  useArchiveTask,
  useUnarchiveTask,
} from './api';
import { STATE_KEY } from './constants';
import styles from './task-move-menu.module.css';

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
  const legal = TRANSITIONS_BY_STATE[state] ?? [];

  const archiveMutation = useArchiveTask();
  const unarchiveMutation = useUnarchiveTask();
  const isArchived = archivedAt != null && archivedAt > 0;

  const handleSelect = (transition: TransitionName) => {
    setOpen(false);
    onTransition(transition, LANDING_STATE[transition]);
  };

  const handleArchive = () => {
    if (!taskId) return;
    setOpen(false);

    if (isArchived) {
      unarchiveMutation.mutate(taskId, {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('archive.unarchived', { ns: 'labels' }) });
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
          toaster.show({ tone: 'success', message: t('archive.archived', { ns: 'labels' }) });
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
                className={styles.menuItem}
                onClick={() => handleSelect(name)}
                onKeyDown={(e) => handleKeyDown(e, idx)}
              >
                <span>{t(`tasks.transitions.${name}`)}</span>
                <span className={styles.menuItemLanding}>→ {t(STATE_KEY[landing])}</span>
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
