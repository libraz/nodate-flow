/**
 * TaskStatusBar — a contained strip under the task title, reading left to
 * right: the current state (a dot and a label, not a control), then the
 * transitions the state machine allows from it. The recommended forward
 * transition is the primary button, the others are quiet ghost buttons, and cancel lives in the "more" menu next
 * to "create calendar event". The mutation lives here; the parent only passes
 * which task and state it renders.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Button from '@nodate-flow/ui/primitives/button';
import Popover from '@nodate-flow/ui/primitives/popover';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { BP } from '@nodate-flow/ui/tokens/breakpoints';
import {
  ArrowRight,
  Ban,
  CalendarPlus,
  Check,
  type LucideIcon,
  MoreHorizontal,
  Play,
} from 'lucide-react';
import {
  type CSSProperties,
  type KeyboardEvent,
  type ReactElement,
  Suspense,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import {
  landingStateFor,
  type TaskDerivedState,
  TRANSITIONS_BY_STATE,
  type TransitionName,
  useSubtasksQuery,
  useTransitionTask,
} from './api';
import { STATE_KEY, TRANSITION_KEY } from './constants';
import EventFromTaskDialog from './event-from-task/event-from-task-dialog';
import StateDot from './state-dot';
import css from './task-status-bar.module.css';

// The forward action worth nudging toward: first of these that is legal from the current state.
const RECOMMENDED_PRIORITY: readonly TransitionName[] = ['complete', 'submit', 'start'];

const RECOMMENDED_ICON: Partial<Record<TransitionName, LucideIcon>> = {
  complete: Check,
  submit: ArrowRight,
  start: Play,
};

const NARROW_QUERY = `(max-width: ${BP.sm - 1}px)`;

/** The nearest ancestor that scrolls vertically, or null for the viewport. */
function scrollParent(el: HTMLElement): HTMLElement | null {
  for (let node = el.parentElement; node; node = node.parentElement) {
    const { overflowY } = getComputedStyle(node);
    if (overflowY === 'auto' || overflowY === 'scroll') return node;
  }
  return null;
}

function subscribeNarrow(notify: () => void): () => void {
  const mql = window.matchMedia(NARROW_QUERY);
  mql.addEventListener('change', notify);
  return () => mql.removeEventListener('change', notify);
}

function readNarrow(): boolean {
  return window.matchMedia(NARROW_QUERY).matches;
}

export interface TaskStatusBarProps {
  taskId: string;
  projectId: string;
  workspaceId: string;
  state: TaskDerivedState;
  /** Title of the task; shown at the start of the strip while it is stuck to the top. */
  title: string;
}

/** How far through its subtasks the task is: a small pie and `done/total`; nothing without subtasks. */
function SubtaskProgress({
  taskId,
  workspaceId,
  compact,
}: {
  taskId: string;
  workspaceId: string;
  /** Drops the label text, keeping the ring and the count. */
  compact: boolean;
}): ReactElement | null {
  const { t } = useTranslation('common');
  const { data } = useSubtasksQuery(taskId, workspaceId);
  const total = data.total;
  if (total === 0) return null;
  const done = data.tasks.filter((task) => task.derivedState === 'done').length;
  const style = { '--progress': `${Math.round((done / total) * 100)}%` } as CSSProperties;
  return (
    <span
      className={css.progress}
      {...(compact
        ? { role: 'img', 'aria-label': `${t('tasks.detail.subtasks.title')} ${done}/${total}` }
        : {})}
    >
      <span className={css.ring} style={style} aria-hidden />
      {compact ? null : t('tasks.detail.subtasks.title')}
      <span className={css.progressCount}>
        {done}/{total}
      </span>
    </span>
  );
}

export default function TaskStatusBar({
  taskId,
  projectId,
  workspaceId,
  state,
  title,
}: TaskStatusBarProps): ReactElement {
  const { t } = useTranslation('common');
  const transition = useTransitionTask();
  const pending = transition.isPending;
  // Only the clicked control shows the spinner while every control is locked.
  const pendingName = pending ? transition.variables?.transition : undefined;
  const [menuOpen, setMenuOpen] = useState(false);
  const [eventDialogOpen, setEventDialogOpen] = useState(false);
  const menuItemsRef = useRef<(HTMLButtonElement | null)[]>([]);
  const rootRef = useRef<HTMLDivElement>(null);
  const recommendedRef = useRef<HTMLButtonElement | null>(null);
  const moreRef = useRef<HTMLButtonElement | null>(null);
  const actedRef = useRef(false);
  const [stuck, setStuck] = useState(false);
  const narrow = useSyncExternalStore(subscribeNarrow, readNarrow, () => false);
  // The state label settles when it changes after mount, so a transition reads as the click's result.
  const [initialState] = useState(state);

  // The strip sticks at -1px, so once it is stuck its box is clipped and no longer fully visible.
  useEffect(() => {
    const el = rootRef.current;
    if (!el) return;
    // The strip sticks 1px above its resting offset; shrinking the root by that offset plus 1px
    // makes the stuck strip a pixel short of fully visible.
    const offset = Number.parseFloat(getComputedStyle(el).insetBlockStart) || 0;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (!entry) return;
        const rootTop = entry.rootBounds?.top ?? 0;
        const rootHeight = entry.rootBounds?.height ?? window.innerHeight;
        setStuck(
          entry.intersectionRatio < 1 && entry.boundingClientRect.top < rootTop + rootHeight / 2,
        );
      },
      { root: scrollParent(el), rootMargin: `${-(offset + 1)}px 0px 0px 0px`, threshold: [1] },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  // The clicked button may be gone once the state changes: keep focus on the strip.
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs when the state changes after a transition
  useEffect(() => {
    if (!actedRef.current) return;
    actedRef.current = false;
    (recommendedRef.current ?? moreRef.current)?.focus();
  }, [state]);

  const handleTransition = (name: TransitionName): void => {
    if (pending) return;
    actedRef.current = true;
    transition.mutate(
      { id: taskId, transition: name, projectId },
      {
        onError: (err) => {
          actedRef.current = false;
          toaster.show({
            tone: 'warning',
            message: formatApiError(err, t, 'tasks.errors.illegal_transition'),
          });
        },
      },
    );
  };

  const legal = (TRANSITIONS_BY_STATE[state] ?? []).flatMap((name) => {
    const landing = landingStateFor(state, name);
    return landing ? [{ name, landing }] : [];
  });
  const recommended = RECOMMENDED_PRIORITY.find((name) => legal.some((l) => l.name === name));
  const canCancel = legal.some((l) => l.name === 'cancel');
  // The recommended action comes first, right after the state: current state, then next step.
  const visible = legal
    .filter((l) => l.name !== 'cancel')
    .sort((a, b) => Number(b.name === recommended) - Number(a.name === recommended));

  // On a narrow screen only the lead action stays in the strip; the rest move into the menu.
  const shown = narrow ? visible.slice(0, 1) : visible;
  const overflowing = narrow ? visible.slice(1) : [];

  const handleMenuOpenChange = (next: boolean): void => {
    setMenuOpen(next && !pending);
  };

  const handleCreateEvent = (): void => {
    setMenuOpen(false);
    setEventDialogOpen(true);
  };

  const handleCancel = (): void => {
    setMenuOpen(false);
    handleTransition('cancel');
  };

  const handleOverflowTransition = (name: TransitionName): void => {
    setMenuOpen(false);
    handleTransition(name);
  };

  const menuItemCount = overflowing.length + 1 + (canCancel ? 1 : 0);
  const handleMenuKeyDown = (e: KeyboardEvent, idx: number): void => {
    const focusAt = (next: number): void => {
      e.preventDefault();
      menuItemsRef.current[next]?.focus();
    };
    if (e.key === 'ArrowDown') focusAt((idx + 1) % menuItemCount);
    else if (e.key === 'ArrowUp') focusAt((idx - 1 + menuItemCount) % menuItemCount);
    else if (e.key === 'Home') focusAt(0);
    else if (e.key === 'End') focusAt(menuItemCount - 1);
  };

  const cancelPending = pendingName === 'cancel';

  return (
    <div ref={rootRef} className={css.root} data-stuck={stuck ? '' : undefined}>
      {stuck && !narrow ? (
        <span className={css.title} title={title}>
          {title}
        </span>
      ) : null}
      <span key={state} className={cx(css.state, state !== initialState && motion.settle)}>
        <StateDot state={state} />
        {t(STATE_KEY[state])}
      </span>
      <span className={css.divider} aria-hidden />
      <div className={css.actions} aria-busy={pending ? true : undefined}>
        {shown.map(({ name, landing }) => {
          const isRecommended = name === recommended;
          const Icon = isRecommended ? RECOMMENDED_ICON[name] : undefined;
          const clicked = pendingName === name;
          return (
            <Button
              key={name}
              type="button"
              variant={isRecommended ? 'primary' : 'ghost'}
              size="sm"
              className={css.action}
              ref={(node: HTMLButtonElement | null) => {
                if (isRecommended) recommendedRef.current = node;
              }}
              title={`→ ${t(STATE_KEY[landing])}`}
              // aria-disabled, not disabled: keyboard focus stays on the button while it works.
              aria-disabled={pending ? true : undefined}
              data-pending={clicked ? '' : undefined}
              onClick={() => {
                handleTransition(name);
              }}
            >
              {clicked ? (
                <Spinner size="sm" label={t('tasks.detail.transitions.pending')} />
              ) : Icon ? (
                <Icon size={14} aria-hidden />
              ) : null}
              {t(TRANSITION_KEY[name])}
            </Button>
          );
        })}
        <Suspense fallback={null}>
          <SubtaskProgress taskId={taskId} workspaceId={workspaceId} compact={narrow} />
        </Suspense>
      </div>
      <Popover
        open={menuOpen}
        onOpenChange={handleMenuOpenChange}
        placement="bottom-end"
        content={
          <div role="menu" aria-label={t('tasks.detail.transitions.more')} className={css.menuList}>
            {overflowing.map(({ name, landing }, i) => (
              <button
                key={name}
                ref={(el) => {
                  menuItemsRef.current[i] = el;
                }}
                role="menuitem"
                type="button"
                className={css.menuItem}
                title={`→ ${t(STATE_KEY[landing])}`}
                onClick={() => handleOverflowTransition(name)}
                onKeyDown={(e) => handleMenuKeyDown(e, i)}
              >
                <span>{t(TRANSITION_KEY[name])}</span>
              </button>
            ))}
            {overflowing.length > 0 ? <hr className={css.menuDivider} /> : null}
            <button
              ref={(el) => {
                menuItemsRef.current[overflowing.length] = el;
              }}
              role="menuitem"
              type="button"
              className={css.menuItem}
              onClick={handleCreateEvent}
              onKeyDown={(e) => handleMenuKeyDown(e, overflowing.length)}
            >
              <CalendarPlus size={14} aria-hidden />
              <span>{t('tasks.actions.create_event.trigger')}</span>
            </button>
            {canCancel ? (
              <>
                <hr className={css.menuDivider} />
                <button
                  ref={(el) => {
                    menuItemsRef.current[overflowing.length + 1] = el;
                  }}
                  role="menuitem"
                  type="button"
                  className={cx(css.menuItem, css.menuItemDanger)}
                  onClick={handleCancel}
                  onKeyDown={(e) => handleMenuKeyDown(e, overflowing.length + 1)}
                >
                  <Ban size={14} aria-hidden />
                  <span>{t(TRANSITION_KEY.cancel)}</span>
                </button>
              </>
            ) : null}
          </div>
        }
      >
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className={css.moreTrigger}
          ref={moreRef}
          aria-label={t('tasks.detail.transitions.more')}
          aria-haspopup="menu"
          aria-expanded={menuOpen}
          aria-disabled={pending ? true : undefined}
          data-pending={cancelPending ? '' : undefined}
        >
          {cancelPending ? (
            <Spinner size="sm" label={t('tasks.detail.transitions.pending')} />
          ) : (
            <MoreHorizontal size={16} aria-hidden />
          )}
        </Button>
      </Popover>
      {eventDialogOpen ? (
        <EventFromTaskDialog
          taskId={taskId}
          defaultWorkspaceId={workspaceId}
          open={eventDialogOpen}
          onClose={() => setEventDialogOpen(false)}
        />
      ) : null}
    </div>
  );
}
