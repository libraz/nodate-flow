/**
 * /tasks/$taskId — task detail panel (lazy).
 *
 * Two-column layout: main content (title, description, comments) on the
 * left, sidebar (state, priority, due, assignees, transitions) on the
 * right. State transitions go through `useTransitionTask`; description /
 * title / priority / due edits go through `useUpdateTask`.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbSeparator,
} from '@nodate-flow/ui/primitives/breadcrumb';
import Button, { type ButtonVariant } from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Markdown from '@nodate-flow/ui/primitives/markdown';
import SegmentedControl from '@nodate-flow/ui/primitives/segmented-control';
import Separator from '@nodate-flow/ui/primitives/separator';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { BP } from '@nodate-flow/ui/tokens/breakpoints';
import { createLazyFileRoute, getRouteApi, Link } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, Suspense, useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { selectUser, useAuth } from '../features/auth/auth-store';
import ConstraintEditor from '../features/constraints/constraint-editor';
import StateGraph from '../features/constraints/state-graph';
import { useFavoritesQuery } from '../features/favorites/api';
import FavoriteButton from '../features/favorites/favorite-button';
import { useProjectQuery } from '../features/projects/api';
import { useTaskReactionsQuery } from '../features/reactions/api';
import ReactionBar from '../features/reactions/reaction-bar';
import AgentPanel from '../features/tasks/agent-panel/agent-panel';
import AIAgentsSection from '../features/tasks/ai-agents/section';
import AIAgentsSkeleton from '../features/tasks/ai-agents/skeleton';
import {
  TASK_PRIORITIES,
  type TaskDerivedState,
  type TaskPriority,
  TRANSITIONS_BY_STATE,
  type TransitionName,
  useAddTaskActor,
  useAddTaskComment,
  useRemoveTaskActor,
  useTaskActorsQuery,
  useTaskCommentsQuery,
  useTaskDuplicatesQuery,
  useTaskInferStateQuery,
  useTaskQuery,
  useTransitionTask,
  useUpdateTask,
} from '../features/tasks/api';
import CommentRow from '../features/tasks/comment-row';
import { PRIORITY_COLOR, PRIORITY_KEY, STATE_KEY, STATE_TONE } from '../features/tasks/constants';
import DependenciesSection from '../features/tasks/dependencies-section';
import DescriptionHistoryDrawer from '../features/tasks/description-history/description-history-drawer';
import EventFromTaskDialog from '../features/tasks/event-from-task/event-from-task-dialog';
import LinkedEventsSection from '../features/tasks/links/linked-events-section';
import MarkdownEditor from '../features/tasks/markdown-editor';
import TaskAttachments from '../features/tasks/task-attachments';
import TaskStepsPanel from '../features/tasks/task-steps-panel';
import { useTaskTimelineQuery } from '../features/timeline/api';
import ReplayPanel from '../features/timeline/replay-panel';
import TaskMiniTimeline from '../features/timeline/task-mini-timeline';
import { useWorkspaceMembersQuery, useWorkspaceQuery } from '../features/workspaces/api';
import { formatApiError } from '../lib/api-error';
import { formatDate } from '../lib/format';
import { formatDueDate } from '../lib/format-date';

const routeApi = getRouteApi('/_authenticated/tasks/$taskId');

const TRANSITION_KEY: Record<TransitionName, string> = {
  start: 'tasks.detail.transitions.start',
  block: 'tasks.detail.transitions.block',
  unblock: 'tasks.detail.transitions.unblock',
  submit: 'tasks.detail.transitions.submit',
  complete: 'tasks.detail.transitions.complete',
  reopen: 'tasks.detail.transitions.reopen',
  cancel: 'tasks.detail.transitions.cancel',
};

/**
 * String token for each task priority rank. The underlying API model stores
 * priority as an integer 0–4 but the `SegmentedControl` primitive is generic
 * over `T extends string`; these tokens provide a typesafe bridge.
 */
type PriorityToken = 'none' | 'low' | 'medium' | 'high' | 'urgent';

/** Map numeric priority to its segmented-control token. */
const PRIORITY_TO_TOKEN: Record<TaskPriority, PriorityToken> = {
  0: 'none',
  1: 'low',
  2: 'medium',
  3: 'high',
  4: 'urgent',
};

/** Inverse of {@link PRIORITY_TO_TOKEN}. */
const TOKEN_TO_PRIORITY: Record<PriorityToken, TaskPriority> = {
  none: 0,
  low: 1,
  medium: 2,
  high: 3,
  urgent: 4,
};

/**
 * Per-rank tone used by the `colourful` segmented control. Mirrors the
 * ordinal colour ramp of `PRIORITY_COLOR` (neutral → info → success →
 * warning → danger) but maps onto the primitive's tone vocabulary.
 */
const PRIORITY_SEGMENT_TONE: Record<
  TaskPriority,
  'neutral' | 'info' | 'success' | 'warning' | 'danger'
> = {
  0: 'neutral',
  1: 'info',
  2: 'success',
  3: 'warning',
  4: 'danger',
};

const TRANSITION_VARIANT: Partial<Record<TransitionName, ButtonVariant>> = {
  complete: 'primary',
  cancel: 'danger',
};

interface TaskDetailPanelProps {
  id: string;
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
 * project rename guard established in commit a8cae17.
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

  const headingStyle = {
    margin: 0,
    fontFamily: 'var(--font-display)',
    fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
  } as const;

  /**
   * Applied to the `<h1>` while editing so it disappears visually but
   * stays a page-level heading landmark for assistive technology. Mirrors
   * the standard "sr-only" clip pattern used by `<VisuallyHidden>` without
   * wrapping the heading in a non-semantic `<span>`.
   */
  const headingHiddenStyle = {
    ...headingStyle,
    position: 'absolute',
    inlineSize: '1px',
    blockSize: '1px',
    padding: 0,
    overflow: 'hidden',
    clip: 'rect(0, 0, 0, 0)',
    whiteSpace: 'nowrap',
    borderWidth: 0,
  } as const;

  if (!editing) {
    const enterEdit = (): void => {
      setValue(initial);
      setShowEmptyError(false);
      setEditing(true);
    };
    return (
      <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <h1 style={{ ...headingStyle, flex: 1, minInlineSize: 0 }}>{initial}</h1>
        <Suspense fallback={null}>
          <TaskFavoriteStar taskId={id} workspaceId={workspaceId} />
        </Suspense>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={enterEdit}
          aria-label={t('tasks.detail.title_edit_named', { title: initial })}
        >
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
    <div
      style={{ display: 'flex', flexDirection: 'column', gap: '0.375rem', position: 'relative' }}
    >
      {/* Preserve the page-level heading landmark while editing. */}
      <h1 style={headingHiddenStyle}>{initial}</h1>
      <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
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
          style={{ flex: 1 }}
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
          style={{ margin: 0, color: 'var(--nf-color-danger)', fontSize: 'var(--nf-text-sm)' }}
        >
          {t('tasks.validation.title_required')}
        </p>
      ) : null}
    </div>
  );
}

function DescriptionEditor({ id, initial }: { id: string; initial: string }): ReactElement {
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
      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
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
          style={{
            color: isEmpty ? 'var(--nf-color-fg-muted)' : 'var(--nf-color-fg)',
            minBlockSize: '3rem',
            inlineSize: '100%',
            ...(isEmpty
              ? {
                  border: '1px dashed var(--nf-color-border)',
                  borderRadius: '0.5rem',
                  padding: '1rem',
                  cursor: 'pointer',
                  textAlign: 'center' as const,
                }
              : {}),
          }}
        >
          {isEmpty ? t('tasks.detail.description_empty') : <Markdown>{initial}</Markdown>}
        </div>
        {!isEmpty && (
          <div>
            <Button type="button" variant="ghost" onClick={enterEdit}>
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
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
      <MarkdownEditor
        value={value}
        onChange={setValue}
        rows={6}
        autoFocus
        aria-label={t('tasks.detail.description_edit')}
      />
      <div style={{ display: 'flex', gap: '0.5rem', justifyContent: 'flex-end' }}>
        <Button
          type="button"
          variant="ghost"
          onClick={() => {
            setEditing(false);
          }}
        >
          {t('tasks.detail.cancel')}
        </Button>
        <Button
          type="button"
          onClick={() => {
            void handleSave();
          }}
        >
          {t('tasks.detail.save')}
        </Button>
      </div>
    </div>
  );
}

/**
 * DescriptionSection wraps the description card so we can compose the
 * editor with a History trigger that opens the
 * {@link DescriptionHistoryDrawer}. The drawer is mounted lazily — it
 * stays unmounted until the user opens it for the first time, after
 * which the standard react-query cache governs revisits.
 */
function DescriptionSection({ id, initial }: { id: string; initial: string }): ReactElement {
  const { t } = useTranslation('common');
  const [historyOpen, setHistoryOpen] = useState(false);
  return (
    <Card style={{ padding: '1rem' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'flex-end',
          gap: '0.5rem',
          marginBlockEnd: '0.5rem',
        }}
      >
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setHistoryOpen(true)}
          aria-label={t('tasks.history.title')}
        >
          {t('tasks.history.open')}
        </Button>
      </div>
      <DescriptionEditor id={id} initial={initial} />
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
    <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
      <h2 style={{ margin: 0, fontSize: 'var(--nf-text-base)' }}>{t('tasks.actions.title')}</h2>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.5rem' }}>
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
    </Card>
  );
}

function CommentsFeed({ taskId }: { taskId: string }): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: comments } = useTaskCommentsQuery(taskId);
  const add = useAddTaskComment();
  const currentUser = useAuth(selectUser);
  const [body, setBody] = useState('');
  const locale = i18n.resolvedLanguage ?? 'en';

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const trimmed = body.trim();
    if (trimmed.length === 0) return;
    try {
      await add.mutateAsync({ taskId, body: trimmed });
      setBody('');
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.comment_add_failed'),
      });
    }
  };

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <h2 style={{ margin: 0, fontSize: 'var(--nf-text-lg)' }}>{t('tasks.comments.title')}</h2>
      {comments.length === 0 ? (
        <p style={{ color: 'var(--nf-color-fg-muted)', margin: 0 }}>{t('tasks.comments.empty')}</p>
      ) : (
        <ul
          style={{
            listStyle: 'none',
            padding: 0,
            margin: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: '0.75rem',
          }}
        >
          {comments.map((c) => (
            <li key={c.id}>
              <CommentRow
                taskId={taskId}
                comment={c}
                currentUserId={currentUser?.id}
                locale={locale}
              />
            </li>
          ))}
        </ul>
      )}
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}
      >
        <FormField label={t('tasks.comments.title')}>
          {(control) => (
            <Textarea
              {...control}
              value={body}
              onChange={(e) => {
                setBody(e.target.value);
              }}
              placeholder={t('tasks.comments.add_placeholder')}
              rows={3}
            />
          )}
        </FormField>
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button type="submit" disabled={body.trim().length === 0}>
            {t('tasks.comments.add')}
          </Button>
        </div>
      </form>
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
      {assignees.length === 0 ? (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
          {t('tasks.detail.assignees.empty')}
        </p>
      ) : (
        <ul
          style={{
            listStyle: 'none',
            padding: 0,
            margin: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: '0.375rem',
          }}
        >
          {assignees.map((a) => (
            <li
              key={a.id}
              style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}
            >
              <span>{a.displayName}</span>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                aria-label={t('tasks.detail.assignees.remove', { name: a.displayName })}
                onClick={() => {
                  void handleRemove(a.id);
                }}
              >
                ×
              </Button>
            </li>
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
          variant="ghost"
          size="sm"
          onClick={() => {
            setPicking(true);
          }}
        >
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
  const locale = i18n.resolvedLanguage ?? 'en';
  const weekdayLabels = t('common.date.weekdays', { returnObjects: true }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    t('common.date.monthYear', { year, month });
  const update = useUpdateTask();
  const transition = useTransitionTask();

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

  const handleTransition = (name: TransitionName): void => {
    transition.mutate(
      { id, transition: name, projectId },
      {
        onError: (err) => {
          toaster.show({
            tone: 'warning',
            message: formatApiError(err, t, 'tasks.errors.illegal_transition'),
          });
        },
      },
    );
  };

  const legal = TRANSITIONS_BY_STATE[state] ?? [];

  return (
    <aside
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1rem',
        position: 'sticky',
        top: '1rem',
        alignSelf: 'start',
        maxBlockSize: 'calc(100vh - 5rem)',
        overflowY: 'auto',
      }}
    >
      <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={{ color: 'var(--nf-color-fg-muted)' }}>{t('tasks.detail.state_label')}</span>
          <Badge tone={STATE_TONE[state]}>{t(STATE_KEY[state])}</Badge>
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
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1)' }}>
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '0.375rem',
              fontSize: 'var(--nf-text-sm)',
              fontWeight: 'var(--nf-weight-medium)',
              color: 'var(--nf-color-fg)',
            }}
          >
            {t('tasks.detail.priority_label')}
            <span
              aria-hidden
              style={{
                display: 'inline-block',
                width: '0.5rem',
                height: '0.5rem',
                borderRadius: '0.125rem',
                background: PRIORITY_COLOR[priority],
              }}
            />
          </span>
          <SegmentedControl<PriorityToken>
            ariaLabel={t('tasks.priority.aria_label')}
            colourful
            size="sm"
            value={PRIORITY_TO_TOKEN[priority]}
            onChange={(next) => {
              void handlePriorityChange(TOKEN_TO_PRIORITY[next]);
            }}
            options={TASK_PRIORITIES.map((p) => {
              const token = PRIORITY_TO_TOKEN[p];
              return {
                value: token,
                label: t(PRIORITY_KEY[p]),
                tone: PRIORITY_SEGMENT_TONE[p],
              };
            })}
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
              formatMonthYear={formatMonthYear}
              prevLabel={t('calendar.prev')}
              nextLabel={t('calendar.next')}
              triggerLabel={dueOn ? formatDueDate(dueOn, locale) : t('common.date.placeholder')}
            />
          )}
        </FormField>
      </Card>

      <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h2 style={{ margin: 0, fontSize: 'var(--nf-text-base)' }}>
          {t('tasks.detail.transitions.title')}
        </h2>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.5rem' }}>
          {legal.map((name) => (
            <Button
              key={name}
              type="button"
              variant={TRANSITION_VARIANT[name] ?? 'default'}
              onClick={() => {
                handleTransition(name);
              }}
            >
              {t(TRANSITION_KEY[name])}
            </Button>
          ))}
        </div>
      </Card>

      <TaskActionsCard taskId={id} workspaceId={workspaceId} />

      <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h2 style={{ margin: 0, fontSize: 'var(--nf-text-base)' }}>
          {t('tasks.detail.assignees.title')}
        </h2>
        <Suspense
          fallback={
            <div style={{ display: 'flex', justifyContent: 'center', padding: '0.5rem' }}>
              <Spinner label={t('common.loading')} />
            </div>
          }
        >
          <AssigneesSection taskId={id} workspaceId={workspaceId} />
        </Suspense>
      </Card>

      <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <Suspense
          fallback={
            <div style={{ display: 'flex', justifyContent: 'center', padding: '0.5rem' }}>
              <Spinner label={t('common.loading')} />
            </div>
          }
        >
          <StateGraphSection taskId={id} current={state} />
        </Suspense>
      </Card>

      <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <Suspense
          fallback={
            <div style={{ display: 'flex', justifyContent: 'center', padding: '0.5rem' }}>
              <Spinner label={t('common.loading')} />
            </div>
          }
        >
          <ConstraintEditor taskId={id} />
        </Suspense>
      </Card>

      <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <Suspense
          fallback={
            <div style={{ display: 'flex', justifyContent: 'center', padding: '0.5rem' }}>
              <Spinner label={t('common.loading')} />
            </div>
          }
        >
          <ReplayPanel taskId={id} />
        </Suspense>
      </Card>

      <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h2 style={{ margin: 0, fontSize: 'var(--nf-text-base)' }}>
          {t('tasks.detail.infer_state.title')}
        </h2>
        <Suspense
          fallback={
            <div style={{ display: 'flex', justifyContent: 'center', padding: '0.5rem' }}>
              <Spinner label={t('common.loading')} />
            </div>
          }
        >
          <InferStateSection taskId={id} />
        </Suspense>
      </Card>

      <Suspense
        fallback={
          <div style={{ display: 'flex', justifyContent: 'center', padding: '0.5rem' }}>
            <Spinner label={t('common.loading')} />
          </div>
        }
      >
        <TaskStepsPanel taskId={id} workspaceId={workspaceId} />
      </Suspense>

      <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h2 style={{ margin: 0, fontSize: 'var(--nf-text-base)' }}>
          {t('tasks.detail.duplicates.title')}
        </h2>
        <Suspense
          fallback={
            <div style={{ display: 'flex', justifyContent: 'center', padding: '0.5rem' }}>
              <Spinner label={t('common.loading')} />
            </div>
          }
        >
          <RelatedTasksSection taskId={id} />
        </Suspense>
      </Card>

      <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h2 style={{ margin: 0, fontSize: 'var(--nf-text-base)' }}>
          {t('tasks.detail.activity.title')}
        </h2>
        <Suspense
          fallback={
            <div style={{ display: 'flex', justifyContent: 'center', padding: '0.5rem' }}>
              <Spinner label={t('common.loading')} />
            </div>
          }
        >
          <TaskMiniTimeline taskId={id} />
        </Suspense>
      </Card>
    </aside>
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
    <ul
      style={{
        listStyle: 'none',
        padding: 0,
        margin: 0,
        display: 'flex',
        flexDirection: 'column',
        gap: '0.5rem',
      }}
    >
      {data.candidates.map((c) => {
        const isDup = c.classification === 'duplicate';
        return (
          <li key={c.taskId}>
            <a
              href={`/tasks/${c.taskId}`}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '0.5rem',
                color: 'var(--nf-color-fg)',
                textDecoration: 'none',
              }}
            >
              <Badge tone={isDup ? 'danger' : 'warning'}>
                {t(`tasks.detail.duplicates.${isDup ? 'duplicate' : 'related'}`)}
              </Badge>
              <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {c.title}
              </span>
              <span
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 'var(--nf-text-xs)',
                  color: 'var(--nf-color-fg-muted)',
                }}
              >
                {c.score.toFixed(2)}
              </span>
            </a>
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
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.375rem' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
        <Badge tone="accent">{transitionLabel}</Badge>
        <span
          style={{
            fontFamily: 'var(--font-mono)',
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
          fontSize: '0.8125rem',
          color: 'var(--nf-color-fg)',
          display: 'flex',
          gap: '0.375rem',
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
      style={{
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
        display: 'grid',
        gridTemplateColumns: isNarrow ? '1fr' : 'minmax(0, 1fr) minmax(16rem, 22rem)',
        gap: '1.5rem',
      }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem', minInlineSize: 0 }}>
        <Suspense fallback={null}>
          <TaskBreadcrumb workspaceId={task.workspaceId} projectId={task.projectId} />
        </Suspense>
        <TitleEditor id={id} initial={task.title} workspaceId={task.workspaceId} />
        {task.agentContext?.agent ? (
          <AgentPanel taskId={id} agentContext={task.agentContext} locale={locale} />
        ) : null}
        <DescriptionSection id={id} initial={task.description ?? ''} />
        <Suspense fallback={<Skeleton style={{ blockSize: '2rem', inlineSize: '12rem' }} />}>
          <ReactionsSection taskId={id} />
        </Suspense>
        <Separator />
        <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
          <DependenciesSection taskId={id} workspaceId={task.workspaceId} />
        </Suspense>
        <Separator />
        <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
          <LinkedEventsSection taskId={id} workspaceId={task.workspaceId} locale={locale} />
        </Suspense>
        <Separator />
        <Suspense fallback={<AIAgentsSkeleton />}>
          <AIAgentsSection taskId={id} locale={locale} />
        </Suspense>
        <Separator />
        <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
          <TaskAttachments taskId={id} />
        </Suspense>
        <Separator />
        <Suspense fallback={<Skeleton style={{ blockSize: '8rem', inlineSize: '100%' }} />}>
          <CommentsFeed taskId={id} />
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
    <Suspense
      fallback={
        <div style={{ padding: '2rem', display: 'flex', flexDirection: 'column', gap: '1rem' }}>
          <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
          <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <TaskDetailPanel id={taskId} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute('/_authenticated/tasks/$taskId')({
  component: TaskDetailRoute,
});
