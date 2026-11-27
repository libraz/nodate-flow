/**
 * /tasks/$taskId — task detail panel (lazy).
 *
 * Two-column layout: main content (title, description, comments) on the
 * left, sidebar (state, priority, due, assignees, transitions) on the
 * right. State transitions go through `useTransitionTask`; description /
 * title / priority / due edits go through `useUpdateTask`.
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbSeparator,
} from '@nodate-flow/ui/primitives/breadcrumb';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Markdown from '@nodate-flow/ui/primitives/markdown';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import Separator from '@nodate-flow/ui/primitives/separator';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { BP } from '@nodate-flow/ui/tokens/breakpoints';
import { createLazyFileRoute, getRouteApi, Link } from '@tanstack/react-router';
import { History, Pencil, Plus, X } from 'lucide-react';
import { type ReactElement, Suspense, useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import FormActions from '../components/form-actions';
import PageFrame from '../components/page-frame';
import frameStyles from '../components/page-frame.module.css';
import PageHelp from '../components/page-help';
import AiConsequence from '../features/ai-suggestions/ai-consequence';
import { selectUser, useAuth } from '../features/auth/auth-store';
import ConstraintEditor from '../features/constraints/constraint-editor';
import StateGraph from '../features/constraints/state-graph';
import { useFavoritesQuery } from '../features/favorites/api';
import FavoriteButton from '../features/favorites/favorite-button';
import { useTaskLabelsQuery } from '../features/labels/api';
import LabelBadge from '../features/labels/label-badge';
import TaskLabelsField, { TaskLabelsFieldSkeleton } from '../features/labels/task-labels-field';
import { useProjectQuery } from '../features/projects/api';
import { useTaskReactionsQuery } from '../features/reactions/api';
import ReactionBar from '../features/reactions/reaction-bar';
import AgentAssignSection from '../features/tasks/agent-panel/agent-assign-section';
import AgentPanel from '../features/tasks/agent-panel/agent-panel';
import AIAgentsSection from '../features/tasks/ai-agents/section';
import AIAgentsSkeleton from '../features/tasks/ai-agents/skeleton';
import {
  type TaskDerivedState,
  type TaskPriority,
  type TransitionName,
  useAddTaskActor,
  useRemoveTaskActor,
  useTaskActorsQuery,
  useTaskCommentsQuery,
  useTaskDuplicatesQuery,
  useTaskInferStateQuery,
  useTaskQuery,
  useUpdateTask,
} from '../features/tasks/api';
import CommentForm from '../features/tasks/comment-form';
import CommentRow from '../features/tasks/comment-row';
import { PRIORITY_KEY, TRANSITION_KEY } from '../features/tasks/constants';
import DependenciesSection from '../features/tasks/dependencies-section';
import DescriptionHistoryDrawer from '../features/tasks/description-history/description-history-drawer';
import EventFromTaskDialog from '../features/tasks/event-from-task/event-from-task-dialog';
import LinkedEventsSection from '../features/tasks/links/linked-events-section';
import MarkdownEditor from '../features/tasks/markdown-editor';
import PriorityControl from '../features/tasks/priority-control';
import PriorityMark from '../features/tasks/priority-mark';
import SubtasksSection from '../features/tasks/subtasks-section';
import TaskAttachments from '../features/tasks/task-attachments';
import TaskParentLink from '../features/tasks/task-parent-link';
import { TaskSideCard, TaskSideCardHeading } from '../features/tasks/task-side-card';
import TaskSourceLinks from '../features/tasks/task-source-links';
import TaskStateBadge from '../features/tasks/task-state-badge';
import TaskStepsPanel from '../features/tasks/task-steps-panel';
import TaskTransitionCard from '../features/tasks/task-transition-card';
import TaskRoleAssignments from '../features/tasks/workflow/task-role-assignments';
import TaskWorkflowPanel from '../features/tasks/workflow/task-workflow-panel';
import { useTaskTimelineQuery } from '../features/timeline/api';
import ReplayPanel from '../features/timeline/replay-panel';
import TaskMiniTimeline from '../features/timeline/task-mini-timeline';
import { useWorkspaceMembersQuery, useWorkspaceQuery } from '../features/workspaces/api';
import { formatApiError } from '../lib/api-error';
import { formatDate } from '../lib/format';
import { formatDueDate } from '../lib/format-date';
import { onSubmitShortcut } from '../lib/submit-shortcut';
import { useWeekStart } from '../lib/use-week-start';
import css from './_authenticated.tasks.$taskId.module.css';

const routeApi = getRouteApi('/_authenticated/tasks/$taskId');

interface TaskDetailPanelProps {
  id: string;
}

/** Placeholder for a sidebar card body: two text lines, sized like the content. */
function SectionSkeleton(): ReactElement {
  const { t } = useTranslation('common');
  return (
    <div className={css.sectionSkeleton} role="status" aria-label={t('common.loading')}>
      {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
      <Skeleton style={{ blockSize: '1rem', inlineSize: '70%' }} />
      {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
      <Skeleton style={{ blockSize: '1rem', inlineSize: '45%' }} />
    </div>
  );
}

/**
 * TaskFavoriteStar resolves the existing favorite entry (if any) for
 * the current task and renders the FavoriteButton in the correct
 * toggle state. Uses `useFavoritesQuery` (suspense) so the caller must
 * wrap it in a `<Suspense fallback={null}>` boundary.
 */
function TaskFavoriteStar({
  taskId,
  workspaceId,
}: {
  taskId: string;
  workspaceId: string;
}): ReactElement {
  const { data: favorites } = useFavoritesQuery(workspaceId);
  const existing = favorites.find((f) => f.targetType === 'task' && f.targetId === taskId);
  return (
    <FavoriteButton
      workspaceId={workspaceId}
      targetType="task"
      targetId={taskId}
      {...(existing ? { favoriteId: existing.id } : {})}
    />
  );
}

/**
 * TitleEditor renders the task title as a page-level `<h1>` landmark and
 * swaps to an inline text input for editing. The heading element is
 * rendered in both states so assistive technology always observes exactly
 * one `<h1>` on the page — in editing mode the heading is visually hidden
 * but still reachable as a landmark, and the input carries focus.
 *
 * Save is guarded against empty / whitespace-only values and against
 * unchanged values: the button is disabled in those cases, and a keyboard
 * Enter submit on an empty value surfaces an inline `role="alert"`
 * message instead of silently closing the editor. This mirrors the
 * project rename guard (empty and unchanged values are rejected).
 */
function TitleEditor({
  id,
  initial,
  workspaceId,
}: {
  id: string;
  initial: string;
  workspaceId: string;
}): ReactElement {
  const { t } = useTranslation('common');
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(initial);
  const [showEmptyError, setShowEmptyError] = useState(false);
  const update = useUpdateTask();
  const errorId = useId();

  if (!editing) {
    const enterEdit = (): void => {
      setValue(initial);
      setShowEmptyError(false);
      setEditing(true);
    };
    return (
      <div className={css.titleRow}>
        <h1 className={cx(frameStyles.title, css.titleHeading)}>{initial}</h1>
        <PageHelp topic="task-detail" />
        <Suspense fallback={<Skeleton className={css.starSkeleton} />}>
          <TaskFavoriteStar taskId={id} workspaceId={workspaceId} />
        </Suspense>
        <Button
          type="button"
          size="sm"
          onClick={enterEdit}
          aria-label={t('tasks.detail.title_edit_named', { title: initial })}
        >
          <Pencil size={14} aria-hidden />
          {t('tasks.detail.title_edit')}
        </Button>
      </div>
    );
  }

  const trimmed = value.trim();
  const isEmpty = trimmed.length === 0;
  const isUnchanged = trimmed === initial;
  const saveDisabled = update.isPending || isEmpty || isUnchanged;

  const handleSave = async (): Promise<void> => {
    if (isEmpty) {
      setShowEmptyError(true);
      return;
    }
    if (isUnchanged) {
      setEditing(false);
      setShowEmptyError(false);
      return;
    }
    try {
      await update.mutateAsync({ id, patch: { title: trimmed } });
      setEditing(false);
      setShowEmptyError(false);
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.update_failed'),
      });
    }
  };

  return (
    <div className={css.titleEditing}>
      {/* Preserve the page-level heading landmark while editing. */}
      <h1 className={cx(frameStyles.title, css.titleHidden)}>{initial}</h1>
      <div className={cx(css.editorEnter, css.titleEditRow)}>
        <Input
          value={value}
          onChange={(e) => {
            setValue(e.target.value);
            if (showEmptyError && e.target.value.trim().length > 0) {
              setShowEmptyError(false);
            }
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              void handleSave();
            } else if (e.key === 'Escape') {
              e.preventDefault();
              setEditing(false);
              setShowEmptyError(false);
            }
          }}
          autoFocus
          aria-label={t('tasks.detail.title_edit')}
          aria-invalid={showEmptyError ? true : undefined}
          aria-describedby={showEmptyError ? errorId : undefined}
          className={css.titleInput}
        />
        <Button
          type="button"
          disabled={saveDisabled}
          onClick={() => {
            void handleSave();
          }}
        >
          {t('tasks.detail.save')}
        </Button>
        <Button
          type="button"
          variant="ghost"
          onClick={() => {
            setEditing(false);
            setShowEmptyError(false);
          }}
        >
          {t('tasks.detail.cancel')}
        </Button>
      </div>
      {showEmptyError ? (
        <p
          id={errorId}
          role="alert"
          style={{ margin: 0, color: 'var(--nf-color-danger-fg)', fontSize: 'var(--nf-text-sm)' }}
        >
          {t('tasks.validation.title_required')}
        </p>
      ) : null}
    </div>
  );
}

function DescriptionEditor({
  id,
  initial,
  workspaceId,
}: {
  id: string;
  initial: string;
  workspaceId: string;
}): ReactElement {
  const { t } = useTranslation('common');
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(initial);
  const update = useUpdateTask();

  if (!editing) {
    const enterEdit = (): void => {
      setValue(initial);
      setEditing(true);
    };
    const isEmpty = initial.length === 0;
    return (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
        {/* biome-ignore lint/a11y/noStaticElementInteractions: interactive only in the empty state, where it carries role="button" + tabIndex + a keyboard handler; otherwise an inert description renderer. */}
        <div
          role={isEmpty ? 'button' : undefined}
          tabIndex={isEmpty ? 0 : undefined}
          onClick={isEmpty ? enterEdit : undefined}
          onKeyDown={
            isEmpty
              ? (e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    enterEdit();
                  }
                }
              : undefined
          }
          className={isEmpty ? css.descriptionEmpty : undefined}
          // nf-token-override: component dimension, not a spacing step
          style={isEmpty ? undefined : { minBlockSize: '3rem', inlineSize: '100%' }}
        >
          {isEmpty ? t('tasks.detail.description_empty') : <Markdown>{initial}</Markdown>}
        </div>
        {!isEmpty && (
          <div>
            <Button type="button" size="sm" onClick={enterEdit}>
              <Pencil size={14} aria-hidden />
              {t('tasks.detail.description_edit')}
            </Button>
          </div>
        )}
      </div>
    );
  }

  const handleSave = async (): Promise<void> => {
    try {
      await update.mutateAsync({ id, patch: { description: value } });
      setEditing(false);
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.update_failed'),
      });
    }
  };

  return (
    <form
      className={css.editorEnter}
      style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}
      onSubmit={(e) => {
        e.preventDefault();
        void handleSave();
      }}
      onKeyDown={onSubmitShortcut}
    >
      <MarkdownEditor
        value={value}
        onChange={setValue}
        rows={6}
        autoFocus
        workspaceId={workspaceId}
        aria-label={t('tasks.detail.description_edit')}
      />
      <FormActions hint>
        <Button
          type="button"
          variant="ghost"
          onClick={() => {
            setEditing(false);
          }}
        >
          {t('tasks.detail.cancel')}
        </Button>
        <Button type="submit">{t('tasks.detail.save')}</Button>
      </FormActions>
    </form>
  );
}

/**
 * DescriptionSection wraps the description card so we can compose the
 * editor with a History trigger that opens the
 * {@link DescriptionHistoryDrawer}. The drawer is mounted lazily — it
 * stays unmounted until the user opens it for the first time, after
 * which the standard react-query cache governs revisits.
 */
function DescriptionSection({
  id,
  initial,
  workspaceId,
}: {
  id: string;
  initial: string;
  workspaceId: string;
}): ReactElement {
  const { t } = useTranslation('common');
  const [historyOpen, setHistoryOpen] = useState(false);
  return (
    <Card>
      <div className={css.descriptionToolbar}>
        <Button
          type="button"
          size="sm"
          onClick={() => setHistoryOpen(true)}
          aria-label={t('tasks.history.title')}
        >
          <History size={14} aria-hidden />
          {t('tasks.history.open')}
        </Button>
      </div>
      <DescriptionEditor id={id} initial={initial} workspaceId={workspaceId} />
      {historyOpen ? (
        <DescriptionHistoryDrawer
          taskId={id}
          currentBody={initial}
          open={historyOpen}
          onClose={() => setHistoryOpen(false)}
        />
      ) : null}
    </Card>
  );
}

function ReactionsSection({ taskId }: { taskId: string }): ReactElement {
  const currentUser = useAuth(selectUser);
  const { data: reactions } = useTaskReactionsQuery(taskId);
  return (
    <ReactionBar taskId={taskId} reactions={reactions} currentUserId={currentUser?.id ?? ''} />
  );
}

/**
 * TaskActionsCard hosts secondary task actions that don't belong to the
 * state transition cluster. Currently surfaces the
 * "Create calendar event" affordance — the dialog mounts lazily once
 * the actor opens it.
 */
function TaskActionsCard({
  taskId,
  workspaceId,
}: {
  taskId: string;
  workspaceId: string;
}): ReactElement {
  const { t } = useTranslation('common');
  const [eventDialogOpen, setEventDialogOpen] = useState(false);
  return (
    <TaskSideCard>
      <TaskSideCardHeading title={t('tasks.actions.title')} />
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--nf-space-2)' }}>
        <Button type="button" variant="default" onClick={() => setEventDialogOpen(true)}>
          {t('tasks.actions.create_event.trigger')}
        </Button>
      </div>
      {eventDialogOpen ? (
        <EventFromTaskDialog
          taskId={taskId}
          defaultWorkspaceId={workspaceId}
          open={eventDialogOpen}
          onClose={() => setEventDialogOpen(false)}
        />
      ) : null}
    </TaskSideCard>
  );
}

function CommentsFeed({
  taskId,
  workspaceId,
}: {
  taskId: string;
  workspaceId: string;
}): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: comments } = useTaskCommentsQuery(taskId);
  const currentUser = useAuth(selectUser);
  const locale = i18n.resolvedLanguage ?? 'en';
  const { entries, onExited } = usePresenceList(comments, (c) => c.id);
  const staggerFor = useFirstLoadStagger();

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}>
      <h2 style={{ margin: 0, fontSize: 'var(--nf-text-lg)' }}>{t('tasks.comments.title')}</h2>
      {entries.length === 0 ? (
        <p className={css.editorEnter} style={{ color: 'var(--nf-color-fg-muted)', margin: 0 }}>
          {t('tasks.comments.empty')}
        </p>
      ) : (
        <ul className={css.commentList}>
          {entries.map(({ key, item: c, present }, index) => {
            const stagger = staggerFor(index);
            return (
              <PresenceItem
                key={key}
                as="li"
                present={present}
                onExited={() => onExited(key)}
                className={motion.enter}
                {...(stagger ? { style: stagger } : {})}
              >
                <CommentRow
                  taskId={taskId}
                  workspaceId={workspaceId}
                  comment={c}
                  currentUserId={currentUser?.id}
                  locale={locale}
                />
              </PresenceItem>
            );
          })}
        </ul>
      )}
      <CommentForm taskId={taskId} workspaceId={workspaceId} />
    </section>
  );
}

function AssigneesSection({
  taskId,
  workspaceId,
}: {
  taskId: string;
  workspaceId: string;
}): ReactElement {
  const { t } = useTranslation('common');
  const { data: actors } = useTaskActorsQuery(taskId);
  const { data: members } = useWorkspaceMembersQuery(workspaceId);
  const addActor = useAddTaskActor();
  const removeActor = useRemoveTaskActor();
  const [picking, setPicking] = useState(false);

  const assignees = actors.filter((a) => a.role === 'assignee');
  const { entries, onExited } = usePresenceList(assignees, (a) => a.id);
  const assignedUserIds = new Set(assignees.map((a) => a.userId));
  const available = members.filter((m) => !assignedUserIds.has(m.userId));

  const handleAdd = async (userId: string): Promise<void> => {
    if (!userId) return;
    try {
      await addActor.mutateAsync({ taskId, input: { role: 'assignee', userId } });
      setPicking(false);
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.update_failed'),
      });
    }
  };

  const handleRemove = async (actorId: string): Promise<void> => {
    try {
      await removeActor.mutateAsync({ taskId, actorId });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.update_failed'),
      });
    }
  };

  return (
    <>
      {entries.length === 0 ? (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
          {t('tasks.detail.assignees.empty')}
        </p>
      ) : (
        <ul className={css.plainList}>
          {entries.map(({ key, item: a, present }) => (
            <PresenceItem
              key={key}
              as="li"
              present={present}
              onExited={() => onExited(key)}
              className={motion.enter}
            >
              <div className={css.assigneeRow}>
                <span className={css.assigneeName} title={a.displayName}>
                  {a.displayName}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  aria-label={t('tasks.detail.assignees.remove', { name: a.displayName })}
                  onClick={() => {
                    void handleRemove(a.id);
                  }}
                >
                  <X size={14} aria-hidden />
                </Button>
              </div>
            </PresenceItem>
          ))}
        </ul>
      )}
      {picking ? (
        available.length === 0 ? (
          <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
            {t('tasks.detail.assignees.none')}
          </p>
        ) : (
          <Combobox
            aria-label={t('tasks.detail.assignees.add')}
            placeholder={t('tasks.detail.assignees.add')}
            options={available.map((m) => ({ value: m.userId, label: m.displayName }))}
            onChange={(v) => {
              void handleAdd(v);
            }}
          />
        )
      ) : (
        <Button
          type="button"
          size="sm"
          className={css.startAligned}
          onClick={() => {
            setPicking(true);
          }}
        >
          <Plus size={14} aria-hidden />
          {t('tasks.detail.assignees.add')}
        </Button>
      )}
    </>
  );
}

interface SidebarProps {
  id: string;
  projectId: string;
  workspaceId: string;
  state: TaskDerivedState;
  priority: TaskPriority;
  startOn: string | undefined;
  dueOn: string | undefined;
}

function Sidebar({
  id,
  projectId,
  workspaceId,
  state,
  priority,
  startOn,
  dueOn,
}: SidebarProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const weekStart = useWeekStart();
  const { t: tAgents } = useTranslation('aiAgents');
  const { t: tLabels } = useTranslation('labels');
  const labelsLabelId = useId();
  const locale = i18n.resolvedLanguage ?? 'en';
  const weekdayLabels = t('common.date.weekdays', { returnObjects: true }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    t('common.date.monthYear', { year, month });
  const update = useUpdateTask();

  const handlePriorityChange = async (next: TaskPriority): Promise<void> => {
    try {
      await update.mutateAsync({ id, patch: { priority: next } });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.update_failed'),
      });
    }
  };

  /**
   * Renders a toast for a failed date change, mapping the
   * `VALIDATION.BODY.DUE_BEFORE_START` backend invariant violation to a
   * targeted translated message and falling back to the generic
   * `tasks.errors.update_failed` otherwise.
   */
  const toastDateUpdateError = (err: unknown): void => {
    const code = (err as { code?: string } | null)?.code;
    if (code === 'VALIDATION.BODY.DUE_BEFORE_START') {
      toaster.show({
        tone: 'danger',
        message: t('errors:VALIDATION.BODY.DUE_BEFORE_START', { keySeparator: false }),
      });
      return;
    }
    toaster.show({ tone: 'danger', message: formatApiError(err, t, 'tasks.errors.update_failed') });
  };

  const handleStartChange = async (next: string): Promise<void> => {
    try {
      await update.mutateAsync({ id, patch: { startOn: next } });
    } catch (err) {
      toastDateUpdateError(err);
    }
  };

  const handleDueChange = async (next: string): Promise<void> => {
    try {
      await update.mutateAsync({ id, patch: { dueOn: next } });
    } catch (err) {
      toastDateUpdateError(err);
    }
  };

  return (
    <aside className={css.aside}>
      <TaskSideCard>
        <div className={css.stateRow}>
          <span className={css.mutedLabel}>{t('tasks.detail.state_label')}</span>
          <TaskStateBadge state={state} />
        </div>
        <Separator />
        {/*
         * Priority picker — rendered as a segmented control (radiogroup) so
         * the five ordinal ranks are visible at once and the active rank is
         * one click away. We do not use `FormField` here because
         * `radiogroup` uses `aria-label` for its accessible name rather than
         * `<label htmlFor>`, so we compose the visible label + colour dot
         * manually to match the surrounding sidebar rhythm.
         */}
        <div className={css.fieldStack}>
          <span className={css.fieldLabel}>
            {t('tasks.detail.priority_label')}
            <PriorityMark priority={priority} />
          </span>
          <PriorityControl
            value={priority}
            onChange={(next) => {
              void handlePriorityChange(next);
            }}
          />
        </div>
        <FormField label={t('tasks.form.start')}>
          {() => (
            <DatePicker
              value={startOn ?? ''}
              onChange={(next) => {
                void handleStartChange(next);
              }}
              weekdayLabels={weekdayLabels}
              weekStart={weekStart}
              formatMonthYear={formatMonthYear}
              prevLabel={t('calendar.prev')}
              nextLabel={t('calendar.next')}
              triggerLabel={startOn ? formatDate(startOn, locale) : t('common.date.placeholder')}
            />
          )}
        </FormField>
        <FormField label={t('tasks.detail.due_label')}>
          {() => (
            <DatePicker
              value={dueOn ?? ''}
              onChange={(next) => {
                void handleDueChange(next);
              }}
              weekdayLabels={weekdayLabels}
              weekStart={weekStart}
              formatMonthYear={formatMonthYear}
              prevLabel={t('calendar.prev')}
              nextLabel={t('calendar.next')}
              triggerLabel={dueOn ? formatDueDate(dueOn, locale) : t('common.date.placeholder')}
            />
          )}
        </FormField>
        <div className={css.fieldStack}>
          <span id={labelsLabelId} className={css.fieldLabel}>
            {tLabels('title')}
          </span>
          <Suspense fallback={<TaskLabelsFieldSkeleton />}>
            <TaskLabelsField taskId={id} workspaceId={workspaceId} labelledBy={labelsLabelId} />
          </Suspense>
        </div>
      </TaskSideCard>

      <TaskTransitionCard taskId={id} projectId={projectId} state={state} />

      <TaskActionsCard taskId={id} workspaceId={workspaceId} />

      <TaskSideCard>
        <TaskSideCardHeading title={t('tasks.detail.assignees.title')} />
        <Suspense fallback={<SectionSkeleton />}>
          <AssigneesSection taskId={id} workspaceId={workspaceId} />
        </Suspense>
      </TaskSideCard>

      <TaskSideCard>
        <TaskSideCardHeading
          title={t('taskWorkflow.rolesHeading')}
          hint={t('taskWorkflow.rolesDescription')}
        />
        <Suspense fallback={<SectionSkeleton />}>
          <TaskRoleAssignments key={id} taskId={id} workspaceId={workspaceId} />
        </Suspense>
      </TaskSideCard>

      <TaskSideCard>
        <TaskSideCardHeading
          title={tAgents('task_detail.agent.assign.title')}
          hint={tAgents('task_detail.agent.assign.description')}
        />
        <Suspense fallback={<SectionSkeleton />}>
          <AgentAssignSection taskId={id} workspaceId={workspaceId} />
        </Suspense>
      </TaskSideCard>

      <TaskSideCard>
        <Suspense fallback={<SectionSkeleton />}>
          <StateGraphSection taskId={id} current={state} />
        </Suspense>
      </TaskSideCard>

      <TaskSideCard>
        <Suspense fallback={<SectionSkeleton />}>
          <ConstraintEditor taskId={id} />
        </Suspense>
      </TaskSideCard>

      <TaskSideCard>
        <Suspense fallback={<SectionSkeleton />}>
          <ReplayPanel taskId={id} />
        </Suspense>
      </TaskSideCard>

      <TaskSideCard>
        <TaskSideCardHeading
          title={t('tasks.detail.infer_state.title')}
          hint={t('tasks.detail.infer_state.hint')}
          marker={<AiConsequence kind="suggests" />}
        />
        <Suspense fallback={<SectionSkeleton />}>
          <InferStateSection taskId={id} />
        </Suspense>
      </TaskSideCard>

      <Suspense fallback={<SectionSkeleton />}>
        <TaskStepsPanel taskId={id} workspaceId={workspaceId} />
      </Suspense>

      <TaskSideCard>
        <TaskSideCardHeading
          title={t('tasks.detail.duplicates.title')}
          hint={t('tasks.detail.duplicates.hint')}
        />
        <Suspense fallback={<SectionSkeleton />}>
          <RelatedTasksSection taskId={id} />
        </Suspense>
      </TaskSideCard>

      <TaskSideCard>
        <TaskSideCardHeading title={t('tasks.detail.activity.title')} />
        <Suspense fallback={<SectionSkeleton />}>
          <TaskMiniTimeline taskId={id} />
        </Suspense>
      </TaskSideCard>
    </aside>
  );
}

/**
 * On narrow screens the sidebar lands below the whole main column, so the
 * key metadata is repeated read-only under the title.
 */
function TaskMetaStrip({
  taskId,
  state,
  priority,
  dueOn,
  locale,
}: {
  taskId: string;
  state: TaskDerivedState;
  priority: TaskPriority;
  dueOn: string | undefined;
  locale: string;
}): ReactElement {
  const { t } = useTranslation('common');
  return (
    <dl className={css.metaStrip}>
      <div className={css.metaItem}>
        <dt className={css.metaLabel}>{t('tasks.detail.state_label')}</dt>
        <dd className={css.metaValue}>
          <TaskStateBadge state={state} />
        </dd>
      </div>
      <div className={css.metaItem}>
        <dt className={css.metaLabel}>{t('tasks.detail.priority_label')}</dt>
        <dd className={css.metaValue}>
          <PriorityMark priority={priority} />
          {t(PRIORITY_KEY[priority])}
        </dd>
      </div>
      {dueOn ? (
        <div className={css.metaItem}>
          <dt className={css.metaLabel}>{t('tasks.detail.due_label')}</dt>
          <dd className={css.metaValue}>{formatDueDate(dueOn, locale)}</dd>
        </div>
      ) : null}
      <Suspense fallback={null}>
        <MetaLabels taskId={taskId} />
      </Suspense>
    </dl>
  );
}

function MetaLabels({ taskId }: { taskId: string }): ReactElement {
  const { t } = useTranslation('labels');
  const { data: labels } = useTaskLabelsQuery(taskId);
  return (
    <div className={css.metaItem}>
      <dt className={css.metaLabel}>{t('title')}</dt>
      <dd className={cx(css.metaValue, css.metaValueWrap)}>
        {labels.length === 0 ? (
          <span className={css.metaNone}>{t('task.none')}</span>
        ) : (
          labels.map((label) => <LabelBadge key={label.id} name={label.name} color={label.color} />)
        )}
      </dd>
    </div>
  );
}

function RelatedTasksSection({ taskId }: { taskId: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data } = useTaskDuplicatesQuery(taskId);
  if (data.candidates.length === 0) {
    return (
      <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-sm)' }}>
        {t('tasks.detail.duplicates.empty')}
      </p>
    );
  }
  return (
    <ul className={css.plainList}>
      {data.candidates.map((c) => {
        const isDup = c.classification === 'duplicate';
        return (
          <li key={c.taskId}>
            <Link to="/tasks/$taskId" params={{ taskId: c.taskId }} className={css.relatedLink}>
              <Badge tone={isDup ? 'danger' : 'warning'}>
                {isDup
                  ? t('tasks.detail.duplicates.duplicate')
                  : t('tasks.detail.duplicates.related')}
              </Badge>
              <span className={css.relatedTitle} title={c.title}>
                {c.title}
              </span>
              <span className={css.score}>{c.score.toFixed(2)}</span>
            </Link>
          </li>
        );
      })}
    </ul>
  );
}

/**
 * StateGraphSection renders the task state graph with the set of
 * external signal sources that have actually fired on this task
 * highlighted. The active set is derived from `signal.attached` events
 * in the task timeline — every such event carries `payload.source`
 * matching the StateGraph node ids (`github` / `slack` / `google`).
 */
function StateGraphSection({
  taskId,
  current,
}: {
  taskId: string;
  current: TaskDerivedState;
}): ReactElement {
  const { data } = useTaskTimelineQuery(taskId, { kind: ['signal.attached'], limit: 100 });
  const active = new Set<string>();
  for (const ev of data.events) {
    const payload = ev.payload;
    if (payload && typeof payload === 'object' && 'source' in payload) {
      const src = String((payload as { source?: unknown }).source ?? '');
      if (src.length > 0) active.add(src);
    }
  }
  return <StateGraph current={current} activeSources={active} />;
}

function InferStateSection({ taskId }: { taskId: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data } = useTaskInferStateQuery(taskId);
  if (!data.proposal) {
    return (
      <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-sm)' }}>
        {t('tasks.detail.infer_state.empty')}
      </p>
    );
  }
  const { transition, confidence, reason } = data.proposal;
  const transitionKey = TRANSITION_KEY[transition as TransitionName];
  const transitionLabel = transitionKey ? t(transitionKey) : transition;
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1-5)' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}>
        <Badge tone="accent">{transitionLabel}</Badge>
        <span
          style={{
            fontFamily: 'var(--nf-font-mono)',
            fontSize: 'var(--nf-text-xs)',
            color: 'var(--nf-color-fg-muted)',
          }}
        >
          {confidence.toFixed(2)}
        </span>
      </div>
      <p
        style={{
          margin: 0,
          fontSize: 'var(--nf-text-supporting)',
          color: 'var(--nf-color-fg)',
          display: 'flex',
          gap: 'var(--nf-space-1-5)',
          flexWrap: 'wrap',
        }}
      >
        <strong>{t('tasks.detail.infer_state.reason_label')}</strong>
        <span>{reason}</span>
      </p>
    </div>
  );
}

function TaskBreadcrumbInner({
  workspaceId,
  projectId,
}: {
  workspaceId: string;
  projectId: string;
}): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspace } = useWorkspaceQuery(workspaceId);
  const { data: project } = useProjectQuery(projectId);
  return (
    <Breadcrumb label={t('common.breadcrumb')}>
      <BreadcrumbItem asChild>
        <Link to="/workspaces/$id" params={{ id: workspaceId }}>
          {workspace.name}
        </Link>
      </BreadcrumbItem>
      <BreadcrumbSeparator />
      <BreadcrumbItem asChild>
        <Link
          to="/workspaces/$id/projects/$projectId/tasks"
          params={{ id: workspaceId, projectId }}
        >
          {project.name}
        </Link>
      </BreadcrumbItem>
    </Breadcrumb>
  );
}

function TaskBreadcrumb({
  workspaceId,
  projectId,
}: {
  workspaceId: string;
  projectId: string;
}): ReactElement | null {
  if (!workspaceId || !projectId) return null;
  return <TaskBreadcrumbInner workspaceId={workspaceId} projectId={projectId} />;
}

/**
 * Tracks whether the viewport is narrower than the `sm` breakpoint.
 *
 * Mirrors the `useIsMobile` pattern from
 * `apps/flow-web/src/components/layout/sidebar.tsx` so the detail panel can
 * flip an inline grid-template from two columns to one. An inline style cannot
 * be targeted with `@media`, so the decision is made in JS.
 */
function useIsNarrow(): boolean {
  const [narrow, setNarrow] = useState(
    () => typeof window !== 'undefined' && window.innerWidth < BP.sm,
  );
  useEffect(() => {
    const mq = window.matchMedia(`(max-width: ${BP.sm - 1}px)`);
    const onChange = (e: MediaQueryListEvent): void => setNarrow(e.matches);
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, []);
  return narrow;
}

function TaskDetailPanel({ id }: TaskDetailPanelProps): ReactElement {
  const { data: task } = useTaskQuery(id);
  const { i18n } = useTranslation();
  const state = task.derivedState as TaskDerivedState;
  const priority = (task.priority as TaskPriority) ?? 0;
  const isNarrow = useIsNarrow();
  const locale = i18n.resolvedLanguage ?? i18n.language ?? 'en';

  return (
    <section
      className={css.panel}
      style={{
        display: 'grid',
        gridTemplateColumns: isNarrow ? '1fr' : 'minmax(0, 1fr) minmax(16rem, 22rem)',
        gap: 'var(--nf-space-6)',
      }}
    >
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-6)',
          minInlineSize: 0,
        }}
      >
        <Suspense fallback={<Skeleton className={css.breadcrumbSkeleton} />}>
          <TaskBreadcrumb workspaceId={task.workspaceId} projectId={task.projectId} />
        </Suspense>
        {task.parentTaskId ? <TaskParentLink parentId={task.parentTaskId} /> : null}
        <TitleEditor id={id} initial={task.title} workspaceId={task.workspaceId} />
        {isNarrow ? (
          <TaskMetaStrip
            taskId={id}
            state={state}
            priority={priority}
            dueOn={task.dueOn}
            locale={locale}
          />
        ) : null}
        {task.agentContext?.agent ? (
          <AgentPanel taskId={id} agentContext={task.agentContext} locale={locale} />
        ) : null}
        <DescriptionSection
          id={id}
          initial={task.description ?? ''}
          workspaceId={task.workspaceId}
        />
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Suspense fallback={<Skeleton style={{ blockSize: '2rem', inlineSize: '12rem' }} />}>
          <ReactionsSection taskId={id} />
        </Suspense>
        <Separator />
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
          <TaskAttachments taskId={id} />
        </Suspense>
        <Separator />
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Suspense fallback={<Skeleton style={{ blockSize: '8rem', inlineSize: '100%' }} />}>
          <CommentsFeed taskId={id} workspaceId={task.workspaceId} />
        </Suspense>
        <Suspense fallback={<SectionSkeleton />}>
          <TaskSourceLinks taskId={id} />
        </Suspense>
        <TaskWorkflowPanel
          key={id}
          taskId={id}
          workspaceId={task.workspaceId}
          projectId={task.projectId}
        />
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
          <SubtasksSection
            taskId={id}
            projectId={task.projectId}
            workspaceId={task.workspaceId}
            visibility={task.visibility}
            parentTaskId={task.parentTaskId}
          />
        </Suspense>
        <Separator />
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
          <DependenciesSection taskId={id} workspaceId={task.workspaceId} />
        </Suspense>
        <Separator />
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
          <LinkedEventsSection taskId={id} workspaceId={task.workspaceId} locale={locale} />
        </Suspense>
        <Separator />
        <Suspense fallback={<AIAgentsSkeleton />}>
          <AIAgentsSection taskId={id} locale={locale} />
        </Suspense>
      </div>
      <Sidebar
        id={id}
        projectId={task.projectId}
        workspaceId={task.workspaceId}
        state={state}
        priority={priority}
        startOn={task.startedOn ?? undefined}
        dueOn={task.dueOn}
      />
    </section>
  );
}

function TaskDetailRoute(): ReactElement {
  const { taskId } = routeApi.useParams();
  return (
    <PageFrame>
      <Suspense
        fallback={
          <div className={css.fallback}>
            <div className={css.fallbackColumn}>
              {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
              <Skeleton style={{ blockSize: '1rem', inlineSize: '12rem' }} />
              {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
              <Skeleton style={{ blockSize: '2.25rem', inlineSize: '60%' }} />
              {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
              <Skeleton style={{ blockSize: '10rem', inlineSize: '100%' }} />
            </div>
            <div className={css.fallbackColumn}>
              {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
              <Skeleton style={{ blockSize: '14rem', inlineSize: '100%' }} />
              {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
              <Skeleton style={{ blockSize: '6rem', inlineSize: '100%' }} />
            </div>
          </div>
        }
      >
        <TaskDetailPanel id={taskId} />
      </Suspense>
    </PageFrame>
  );
}

export const Route = createLazyFileRoute('/_authenticated/tasks/$taskId')({
  component: TaskDetailRoute,
});
