/**
 * TaskBoardView — Kanban board grouped by `derivedState`.
 *
 * Cards are dragged between columns with `@dnd-kit/core`; the column under the
 * drop picks the transition (first legal verb that lands there) and posts it
 * through the same mutation as the card's move menu, which stays as the
 * keyboard and screen-reader alternative. Same-column and illegal drops do
 * nothing. Each column scrolls on its own inside a viewport-bound board.
 */

import {
  type Announcements,
  type CollisionDetection,
  DndContext,
  type DragEndEvent,
  DragOverlay,
  type DragStartEvent,
  type KeyboardCoordinateGetter,
  KeyboardSensor,
  MouseSensor,
  pointerWithin,
  rectIntersection,
  TouchSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
} from '@dnd-kit/core';
import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Card from '@nodate-flow/ui/primitives/card';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { useNavigate } from '@tanstack/react-router';
import { PanelRightClose, PanelRightOpen } from 'lucide-react';
import {
  type DOMAttributes,
  type ReactElement,
  type TouchEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { type Label, useLabelsQuery } from '../labels/api';
import {
  computeBlockedByOpen,
  useProjectDependenciesQuery,
  useProjectQuery,
} from '../projects/api';
import {
  landingStateFor,
  TASK_STATES,
  TASKS_QUERY_LIMIT,
  type TaskDerivedState,
  type TaskListItem,
  TRANSITIONS_BY_STATE,
  type TransitionName,
  useTasksQuery,
  useTransitionTask,
} from './api';
import { STATE_KEY, TRANSITION_KEY } from './constants';
import css from './task-board-view.module.css';
import TaskCard, { type TaskCardProps } from './task-card';
import { setBoardCancelledOpen, useBoardCancelledOpen } from './use-board-cancelled-open';
import { useTaskFilters } from './use-task-filters';

export interface TaskBoardViewProps {
  projectId: string;
}

function groupByState(tasks: readonly TaskListItem[]): Record<TaskDerivedState, TaskListItem[]> {
  const groups: Record<TaskDerivedState, TaskListItem[]> = {
    open: [],
    waiting: [],
    review: [],
    done: [],
    cancelled: [],
  };
  for (const task of tasks) {
    const state = task.derivedState as TaskDerivedState;
    const bucket = groups[state];
    if (bucket) bucket.push(task);
  }
  return groups;
}

/** Landing state to the first transition that reaches it from `from`. */
export function transitionsByLanding(
  from: TaskDerivedState,
): Map<TaskDerivedState, TransitionName> {
  const map = new Map<TaskDerivedState, TransitionName>();
  for (const name of TRANSITIONS_BY_STATE[from] ?? []) {
    const landing = landingStateFor(from, name);
    if (landing && !map.has(landing)) map.set(landing, name);
  }
  return map;
}

function isTaskState(value: unknown): value is TaskDerivedState {
  return typeof value === 'string' && (TASK_STATES as readonly string[]).includes(value);
}

/** Label ids resolved against the workspace labels; deleted labels drop out. */
function resolveLabels(task: TaskListItem, labelsById: ReadonlyMap<string, Label>): Label[] {
  return (task.labelIds ?? []).flatMap((id) => {
    const label = labelsById.get(id);
    return label ? [label] : [];
  });
}

function prefersReducedMotion(): boolean {
  return window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
}

const ACTIVATION_DISTANCE = 5;
const TOUCH_DELAY_MS = 250;
const TOUCH_TOLERANCE = 5;
const COLLAPSIBLE: TaskDerivedState = 'cancelled';

/** Pointer position decides the column; the dragged rect covers keyboard drags. */
const detectCollisions: CollisionDetection = (args) => {
  const within = pointerWithin(args);
  return within.length > 0 ? within : rectIntersection(args);
};

/**
 * Left / right arrows jump to the neighbouring column. Up / down keep the
 * current position (the column is the target, so there is nothing to step to).
 */
const columnKeyboardCoordinates: KeyboardCoordinateGetter = (
  event,
  { context: { collisionRect, droppableRects, droppableContainers } },
) => {
  if (!collisionRect) return undefined;
  if (event.code === 'ArrowUp' || event.code === 'ArrowDown') {
    return { x: collisionRect.left, y: collisionRect.top };
  }
  const forward = event.code === 'ArrowRight';
  if (!forward && event.code !== 'ArrowLeft') return undefined;
  const centerX = collisionRect.left + collisionRect.width / 2;
  let best: { left: number; width: number } | null = null;
  let bestDistance = Number.POSITIVE_INFINITY;
  for (const container of droppableContainers.getEnabled()) {
    const rect = droppableRects.get(container.id);
    if (!rect) continue;
    const delta = rect.left + rect.width / 2 - centerX;
    if (forward ? delta <= 1 : delta >= -1) continue;
    if (Math.abs(delta) < bestDistance) {
      best = rect;
      bestDistance = Math.abs(delta);
    }
  }
  if (!best) return undefined;
  return {
    x: best.left + (best.width - collisionRect.width) / 2,
    y: collisionRect.top,
  };
};

interface DragInfo {
  taskId: string;
  from: TaskDerivedState;
}

/**
 * The entrance is picked once, at mount: the first-load stagger ends
 * after a few hundred ms and the class must not flip under a card that
 * has already arrived, or the new animation would replay.
 */
function BoardCard({
  arrivingDone,
  state,
  dragging,
  ...props
}: TaskCardProps & {
  arrivingDone: boolean;
  state: TaskDerivedState;
  dragging: boolean;
}): ReactElement {
  const [entrance] = useState(() => (arrivingDone ? motion.settle : motion.enter));
  const [holding, setHolding] = useState(false);
  const touchStart = useRef<{ x: number; y: number } | null>(null);
  const { setNodeRef, setActivatorNodeRef, attributes, listeners } = useDraggable({
    id: props.task.id,
    data: { from: state },
  });
  const sensorListeners = listeners as DOMAttributes<HTMLDivElement> | undefined;

  // The touch sensor waits out its delay before lifting; the card presses in meanwhile.
  const releaseHold = (): void => {
    touchStart.current = null;
    setHolding(false);
  };
  const handlers: DOMAttributes<HTMLDivElement> = {
    ...sensorListeners,
    onTouchStart: (e: TouchEvent<HTMLDivElement>) => {
      sensorListeners?.onTouchStart?.(e);
      const point = e.touches[0];
      if (!point) return;
      touchStart.current = { x: point.clientX, y: point.clientY };
      setHolding(true);
    },
    onTouchMove: (e: TouchEvent<HTMLDivElement>) => {
      const start = touchStart.current;
      const point = e.touches[0];
      if (!start || !point) return;
      if (Math.hypot(point.clientX - start.x, point.clientY - start.y) > TOUCH_TOLERANCE) {
        releaseHold();
      }
    },
    onTouchEnd: releaseHold,
    onTouchCancel: releaseHold,
  };

  return (
    <TaskCard
      {...props}
      className={entrance}
      dragging={dragging}
      holding={holding && !dragging}
      dragHandle={{
        setNodeRef,
        setActivatorNodeRef,
        describedBy: attributes['aria-describedby'],
        roleDescription: attributes['aria-roledescription'],
        listeners: handlers,
      }}
    />
  );
}

interface BoardColumnListProps {
  state: TaskDerivedState;
  items: TaskListItem[];
  blockedByOpen: Map<string, number>;
  labelsById: ReadonlyMap<string, Label>;
  activeTaskId: string | null;
  staggerFor: (index: number) => React.CSSProperties | undefined;
  onSelect: (taskId: string) => void;
  onTransition: (
    taskId: string,
    transition: TransitionName,
    landingState: TaskDerivedState,
  ) => void;
}

/**
 * One column's cards. A card that leaves (moved to another state,
 * archived) collapses out while its copy enters the destination column,
 * so a move reads as a move; a card that lands in Done settles instead
 * of sliding, which is the completion beat.
 */
function BoardColumnList({
  state,
  items,
  blockedByOpen,
  labelsById,
  activeTaskId,
  staggerFor,
  onSelect,
  onTransition,
}: BoardColumnListProps): ReactElement {
  const { t } = useTranslation('common');
  const { entries, onExited } = usePresenceList(items, (task) => task.id);

  return (
    <div role="list" aria-label={t(STATE_KEY[state])} className={css.dropZone}>
      {entries.length === 0 ? (
        <div role="listitem" className={css.emptyColumn}>
          {t('tasks.board.empty_column')}
        </div>
      ) : (
        entries.map(({ key, item: task, present }, index) => {
          const stagger = staggerFor(index);
          return (
            <PresenceItem
              key={key}
              role="listitem"
              present={present}
              onExited={() => onExited(key)}
            >
              <BoardCard
                task={task}
                state={state}
                dragging={activeTaskId === task.id}
                labels={resolveLabels(task, labelsById)}
                blockedByOpenCount={blockedByOpen.get(task.id) ?? 0}
                onSelect={onSelect}
                onTransition={onTransition}
                arrivingDone={!stagger && state === 'done'}
                {...(stagger ? { style: stagger } : {})}
              />
            </PresenceItem>
          );
        })
      )}
    </div>
  );
}

type DropStatus = 'origin' | 'legal' | 'over' | 'illegal';

function dropStatusFor(
  state: TaskDerivedState,
  drag: DragInfo | null,
  legalTargets: ReadonlyMap<TaskDerivedState, TransitionName>,
  isOver: boolean,
): DropStatus | undefined {
  if (!drag) return undefined;
  if (state === drag.from) return 'origin';
  if (!legalTargets.has(state)) return 'illegal';
  return isOver ? 'over' : 'legal';
}

interface BoardColumnProps extends BoardColumnListProps {
  count: number;
  drag: DragInfo | null;
  legalTargets: ReadonlyMap<TaskDerivedState, TransitionName>;
  /** Present on the collapsible column: folds it back into its rail. */
  onCollapse?: (() => void) | undefined;
}

/** The whole column is the drop target; its `data-drop` drives the drag-time styling. */
function BoardColumn({
  count,
  drag,
  legalTargets,
  onCollapse,
  ...list
}: BoardColumnProps): ReactElement {
  const { t } = useTranslation('common');
  const { setNodeRef, isOver } = useDroppable({ id: list.state });
  const drop = dropStatusFor(list.state, drag, legalTargets, isOver);
  const hoveredVerb = drop === 'over' ? legalTargets.get(list.state) : undefined;
  const stateLabel = t(STATE_KEY[list.state]);

  return (
    <section
      ref={setNodeRef}
      aria-label={stateLabel}
      className={css.column}
      data-state={list.state}
      data-drop={drop}
    >
      <Card className={css.headerCard}>
        <header className={css.columnHeader}>
          <span className={css.columnHeaderLabel}>{stateLabel}</span>
          {hoveredVerb ? (
            <span className={css.dropHint} aria-hidden="true">
              {t('tasks.board.dnd.drop_hint', { verb: t(TRANSITION_KEY[hoveredVerb]) })}
            </span>
          ) : (
            <span className={css.columnHeaderCount}>{count}</span>
          )}
          {onCollapse ? (
            <button
              type="button"
              className={css.railToggle}
              aria-expanded="true"
              aria-label={t('tasks.board.rail.collapse', { state: stateLabel })}
              title={t('tasks.board.rail.collapse', { state: stateLabel })}
              onClick={onCollapse}
            >
              <PanelRightClose size={16} aria-hidden />
            </button>
          ) : null}
        </header>
      </Card>
      <div className={css.zone}>
        <BoardColumnList {...list} />
      </div>
    </section>
  );
}

interface BoardRailProps {
  state: TaskDerivedState;
  count: number;
  drag: DragInfo | null;
  legalTargets: ReadonlyMap<TaskDerivedState, TransitionName>;
  onExpand: () => void;
}

/** A collapsed column: a narrow strip that still accepts drops. */
function BoardRail({ state, count, drag, legalTargets, onExpand }: BoardRailProps): ReactElement {
  const { t } = useTranslation('common');
  const { setNodeRef, isOver } = useDroppable({ id: state });
  const drop = dropStatusFor(state, drag, legalTargets, isOver);
  const stateLabel = t(STATE_KEY[state]);
  const label = t('tasks.board.rail.expand', { state: stateLabel, count });

  return (
    <section ref={setNodeRef} aria-label={stateLabel} className={css.rail} data-drop={drop}>
      <Card className={css.railCard}>
        <button
          type="button"
          className={css.railButton}
          aria-expanded="false"
          aria-label={label}
          title={label}
          onClick={onExpand}
        >
          <PanelRightOpen size={16} aria-hidden />
          <span className={css.railLabel}>{stateLabel}</span>
          <span className={css.columnHeaderCount}>{count}</span>
        </button>
      </Card>
    </section>
  );
}

export default function TaskBoardView({ projectId }: TaskBoardViewProps): ReactElement {
  const { t } = useTranslation('common');
  const navigate = useNavigate();
  const filters = useTaskFilters(projectId);
  const { data: tasks } = useTasksQuery(projectId, filters);
  const { data: edges } = useProjectDependenciesQuery(projectId);
  const { data: project } = useProjectQuery(projectId);
  const { data: labels } = useLabelsQuery(project.workspaceId);
  const blockedByOpen = computeBlockedByOpen(edges);
  const labelsById = new Map(labels.map((label) => [label.id, label]));
  const transition = useTransitionTask();
  const staggerFor = useFirstLoadStagger();
  const boardRef = useRef<HTMLDivElement>(null);
  const cancelledOpen = useBoardCancelledOpen();
  // Set when a keyboard drop moves a card, so focus can follow it into its new column.
  const keyboardDrag = useRef(false);
  const pendingFocus = useRef<{ id: string; to: TaskDerivedState } | null>(null);
  // The click that ends a drag lands on whatever is under the pointer; it must not open a task.
  const justDragged = useRef(false);
  const [drag, setDrag] = useState<DragInfo | null>(null);
  // A drop that moved the card lands through the column's presence motion;
  // only a no-op or cancelled drop flies the overlay back to its slot.
  const [settleBack, setSettleBack] = useState(false);

  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: ACTIVATION_DISTANCE } }),
    useSensor(TouchSensor, {
      activationConstraint: { delay: TOUCH_DELAY_MS, tolerance: TOUCH_TOLERANCE },
    }),
    useSensor(KeyboardSensor, {
      coordinateGetter: columnKeyboardCoordinates,
      // Enter stays free for the title link; Space picks up and drops.
      keyboardCodes: { start: ['Space'], cancel: ['Escape'], end: ['Space'] },
    }),
  );

  const groups = groupByState(tasks);
  const mayBeTruncated = tasks.length >= TASKS_QUERY_LIMIT;
  const legalTargets = drag ? transitionsByLanding(drag.from) : new Map();
  const activeTask = drag ? tasks.find((task) => task.id === drag.taskId) : undefined;

  const handleTransition = useCallback(
    (taskId: string, transitionName: TransitionName, landingState: TaskDerivedState): void => {
      transition.mutate(
        {
          id: taskId,
          transition: transitionName,
          projectId,
          optimisticState: landingState,
        },
        {
          onError: (err) => {
            toaster.show({
              tone: 'warning',
              message: formatApiError(err, t, 'tasks.errors.move_failed'),
            });
          },
        },
      );
    },
    [transition, projectId, t],
  );

  const handleSelect = (taskId: string): void => {
    if (justDragged.current) return;
    void navigate({ to: '/tasks/$taskId', params: { taskId } });
  };

  const swallowNextClick = (): void => {
    justDragged.current = true;
    setTimeout(() => {
      justDragged.current = false;
    }, 0);
  };

  const handleDragStart = ({ active, activatorEvent }: DragStartEvent): void => {
    keyboardDrag.current = activatorEvent instanceof KeyboardEvent;
    const from = active.data.current?.from;
    if (isTaskState(from)) setDrag({ taskId: String(active.id), from });
    setSettleBack(false);
  };

  const handleDragEnd = ({ active, over }: DragEndEvent): void => {
    setDrag(null);
    swallowNextClick();
    const from = active.data.current?.from;
    if (!isTaskState(from) || !over || !isTaskState(over.id)) {
      setSettleBack(true);
      return;
    }
    const name = transitionsByLanding(from).get(over.id);
    if (name) {
      handleTransition(String(active.id), name, over.id);
      if (keyboardDrag.current) pendingFocus.current = { id: String(active.id), to: over.id };
    }
    setSettleBack(!name);
  };

  const handleDragCancel = (): void => {
    setDrag(null);
    swallowNextClick();
    setSettleBack(true);
  };

  // The grabbing cursor must win over every element's own cursor while a card is held.
  useEffect(() => {
    if (!drag) return;
    document.body.style.cursor = 'grabbing';
    return () => {
      document.body.style.cursor = '';
    };
  }, [drag]);

  // After a keyboard drop the moved card is a new element in another column: refocus it.
  // The card mounts a frame or two after the optimistic update, so look for it briefly.
  // biome-ignore lint/correctness/useExhaustiveDependencies: restarts when the task list changes
  useEffect(() => {
    const pending = pendingFocus.current;
    if (!pending) return;
    if (pending.to === COLLAPSIBLE && !cancelledOpen) {
      pendingFocus.current = null;
      return;
    }
    let frame = 0;
    let remaining = 30;
    const seek = (): void => {
      const handle = boardRef.current?.querySelector<HTMLElement>(
        `[data-state="${pending.to}"] [data-task-handle="${CSS.escape(pending.id)}"]`,
      );
      if (handle) {
        pendingFocus.current = null;
        // dnd-kit hands focus back to the old (now unmounted) handle after the drop; go after it.
        frame = requestAnimationFrame(() => {
          frame = requestAnimationFrame(() => handle.focus());
        });
      } else if (remaining-- > 0) {
        frame = requestAnimationFrame(seek);
      } else {
        pendingFocus.current = null;
      }
    };
    seek();
    return () => cancelAnimationFrame(frame);
  }, [tasks, cancelledOpen]);

  const titleOf = (id: string | number): string =>
    tasks.find((task) => task.id === id)?.title ?? '';
  const stateName = (id: string | number | undefined, fallback: TaskDerivedState): string =>
    t(STATE_KEY[isTaskState(id) ? id : fallback]);

  const announcements: Announcements = {
    onDragStart: ({ active }) => {
      const from = active.data.current?.from;
      return t('tasks.board.dnd.lifted', {
        title: titleOf(active.id),
        state: stateName(isTaskState(from) ? from : undefined, 'open'),
      });
    },
    onDragOver: ({ active, over }) => {
      const from = active.data.current?.from;
      if (!over || !isTaskState(from) || !isTaskState(over.id) || over.id === from) return '';
      const key = transitionsByLanding(from).has(over.id)
        ? 'tasks.board.dnd.over'
        : 'tasks.board.dnd.over_blocked';
      return t(key, { title: titleOf(active.id), state: t(STATE_KEY[over.id]) });
    },
    onDragEnd: ({ active, over }) => {
      const from = active.data.current?.from;
      if (!over || !isTaskState(from) || !isTaskState(over.id)) {
        return t('tasks.board.dnd.dropped_none', { title: titleOf(active.id) });
      }
      return transitionsByLanding(from).has(over.id)
        ? t('tasks.board.dnd.dropped', { title: titleOf(active.id), state: t(STATE_KEY[over.id]) })
        : t('tasks.board.dnd.dropped_none', { title: titleOf(active.id) });
    },
    onDragCancel: ({ active }) => t('tasks.board.dnd.cancelled', { title: titleOf(active.id) }),
  };

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={detectCollisions}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
      onDragCancel={handleDragCancel}
      accessibility={{
        announcements,
        screenReaderInstructions: { draggable: t('tasks.board.dnd.instructions') },
      }}
    >
      {mayBeTruncated ? (
        <p className={css.limitNotice}>{t('tasks.limit_notice', { count: TASKS_QUERY_LIMIT })}</p>
      ) : null}
      <div
        ref={boardRef}
        role="region"
        aria-label={t('tasks.views.board')}
        className={css.board}
        data-drag-active={drag ? true : undefined}
        data-rail={!cancelledOpen ? true : undefined}
        style={
          {
            '--col-count': cancelledOpen ? TASK_STATES.length : TASK_STATES.length - 1,
          } as React.CSSProperties
        }
      >
        {TASK_STATES.map((state) => {
          const collapsed = state === COLLAPSIBLE && !cancelledOpen;
          if (collapsed) {
            return (
              <BoardRail
                key={state}
                state={state}
                count={groups[state].length}
                drag={drag}
                legalTargets={legalTargets}
                onExpand={() => setBoardCancelledOpen(true)}
              />
            );
          }
          return (
            <BoardColumn
              key={state}
              state={state}
              items={groups[state]}
              count={groups[state].length}
              blockedByOpen={blockedByOpen}
              labelsById={labelsById}
              activeTaskId={drag?.taskId ?? null}
              staggerFor={staggerFor}
              onSelect={handleSelect}
              onTransition={handleTransition}
              drag={drag}
              legalTargets={legalTargets}
              onCollapse={state === COLLAPSIBLE ? () => setBoardCancelledOpen(false) : undefined}
            />
          );
        })}
      </div>
      {/* Outside the masked board: a mask would clip the fixed-position overlay. */}
      <DragOverlay dropAnimation={settleBack && !prefersReducedMotion() ? undefined : null}>
        {activeTask ? (
          <TaskCard
            task={activeTask}
            labels={resolveLabels(activeTask, labelsById)}
            blockedByOpenCount={blockedByOpen.get(activeTask.id) ?? 0}
            onSelect={handleSelect}
            onTransition={handleTransition}
            overlay
          />
        ) : null}
      </DragOverlay>
    </DndContext>
  );
}
