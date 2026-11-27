/**
 * /tasks/$taskId — task detail panel (lazy).
 *
 * Two-column layout: main content (title, status bar, description and the
 * other sections, comments last) on the left, sidebar (details, AI and
 * automation, analysis, activity) on the right. State transitions go
 * through `TaskStatusBar`; description / title / priority / due edits go
 * through `useUpdateTask`.
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
import Collapse from '@nodate-flow/ui/primitives/collapse';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Input from '@nodate-flow/ui/primitives/input';
import Markdown from '@nodate-flow/ui/primitives/markdown';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { BP } from '@nodate-flow/ui/tokens/breakpoints';
import { createLazyFileRoute, getRouteApi, Link } from '@tanstack/react-router';
import {
  Bot,
  CalendarDays,
  History,
  Lightbulb,
  ListTree,
  Pencil,
  Plus,
  ShieldCheck,
  X,
} from 'lucide-react';
import { type ReactElement, type ReactNode, Suspense, useEffect, useId, useState } from 'react';
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
import ActivityTabs from '../features/tasks/activity-tabs';
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
import AutomationTile from '../features/tasks/automation-tile';
import CommentForm from '../features/tasks/comment-form';
import CommentRow from '../features/tasks/comment-row';
import { PRIORITY_KEY, TRANSITION_KEY } from '../features/tasks/constants';
import DependenciesSection from '../features/tasks/dependencies-section';
import DescriptionHistoryDrawer from '../features/tasks/description-history/description-history-drawer';
import { useCollapsibleState } from '../features/tasks/links/hooks/use-collapsible-state';
import LinkedEventsSection from '../features/tasks/links/linked-events-section';
import MarkdownEditor from '../features/tasks/markdown-editor';
import PriorityMark from '../features/tasks/priority-mark';
import PriorityPicker from '../features/tasks/priority-picker';
import fieldStyles from '../features/tasks/property-field.module.css';
import SubtasksSection from '../features/tasks/subtasks-section';
import TaskActionButton from '../features/tasks/task-action-button';
import TaskAttachments from '../features/tasks/task-attachments';
import TaskParentLink from '../features/tasks/task-parent-link';
import TaskSectionHeading, { TaskDisclosureToggle } from '../features/tasks/task-section-heading';
import {
  HintTip,
  TaskSideCard,
  TaskSideCardHeading,
  TaskSideGroup,
} from '../features/tasks/task-side-card';
import TaskSourceLinks from '../features/tasks/task-source-links';
import TaskStatusBar from '../features/tasks/task-status-bar';
import TaskStepsPanel from '../features/tasks/task-steps-panel';
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
  // Set once an edit ends, so the pencil that remounts takes focus back.
  const [returnFocus, setReturnFocus] = useState(false);
  const update = useUpdateTask();
  const errorId = useId();

  if (!editing) {
    const enterEdit = (): void => {
      setValue(initial);
      setShowEmptyError(false);
      setReturnFocus(false);
      setEditing(true);
    };
    const title = t('tasks.detail.title_edit_named', { title: initial });
    return (
      <div className={css.titleRow}>
        {/* biome-ignore lint/a11y/useKeyWithClickEvents: see above, the pencil button is the keyboard path. */}
        <h1 className={cx(frameStyles.title, css.titleHeading)} onClick={enterEdit}>
          {initial}
        </h1>
        <div className={css.titleActions}>
          <PageHelp topic="task-detail" />
          <Suspense fallback={<Skeleton className={css.starSkeleton} />}>
            <TaskFavoriteStar taskId={id} workspaceId={workspaceId} />
          </Suspense>
          <Tooltip content={t('tasks.detail.title_edit')}>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className={css.iconButton}
              autoFocus={returnFocus}
              onClick={enterEdit}
              aria-label={title}
            >
              <Pencil size={14} aria-hidden />
            </Button>
          </Tooltip>
        </div>
      </div>
    );
  }

  const trimmed = value.trim();
  const isEmpty = trimmed.length === 0;
  const isUnchanged = trimmed === initial;
  const saveDisabled = update.isPending || isEmpty || isUnchanged;

  const closeEditor = (): void => {
    setReturnFocus(true);
    setEditing(false);
    setShowEmptyError(false);
  };

  const handleSave = async (): Promise<void> => {
    if (isEmpty) {
      setShowEmptyError(true);
      return;
    }
    if (isUnchanged) {
      closeEditor();
      return;
    }
    try {
      await update.mutateAsync({ id, patch: { title: trimmed } });
      closeEditor();
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
              closeEditor();
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
            closeEditor();
          }}
        >
          {t('tasks.detail.cancel')}
        </Button>
      </div>
      {showEmptyError ? (
        <p id={errorId} role="alert" className={css.titleError}>
          {t('tasks.validation.title_required')}
        </p>
      ) : null}
    </div>
  );
}

function DescriptionForm({
  id,
  initial,
  workspaceId,
  onClose,
}: {
  id: string;
  initial: string;
  workspaceId: string;
  onClose: () => void;
}): ReactElement {
  const { t } = useTranslation('common');
  const [value, setValue] = useState(initial);
  const update = useUpdateTask();

  const handleSave = async (): Promise<void> => {
    try {
      await update.mutateAsync({ id, patch: { description: value } });
      onClose();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.update_failed'),
      });
    }
  };

  return (
    <form
      className={cx(css.editorEnter, css.descriptionForm)}
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
        <Button type="button" variant="ghost" onClick={onClose}>
          {t('tasks.detail.cancel')}
        </Button>
        <Button type="submit">{t('tasks.detail.save')}</Button>
      </FormActions>
    </form>
  );
}

function DescriptionEditor({
  id,
  initial,
  workspaceId,
  editing,
  onEditingChange,
}: {
  id: string;
  initial: string;
  workspaceId: string;
  editing: boolean;
  onEditingChange: (next: boolean) => void;
}): ReactElement {
  const { t } = useTranslation('common');

  if (editing) {
    return (
      <DescriptionForm
        id={id}
        initial={initial}
        workspaceId={workspaceId}
        onClose={() => {
          onEditingChange(false);
        }}
      />
    );
  }

  const isEmpty = initial.length === 0;
  const enterEdit = (): void => {
    onEditingChange(true);
  };
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: interactive only in the empty state, where it carries role="button" + tabIndex + a keyboard handler; otherwise an inert description renderer.
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
      className={isEmpty ? css.descriptionEmpty : css.descriptionBody}
    >
      {isEmpty ? t('tasks.detail.description_empty') : <Markdown>{initial}</Markdown>}
    </div>
  );
}

/**
 * DescriptionSection is the column's one card: the heading row carries
 * History and Edit, the card holds the body and, in its footer, the
 * reactions. The history drawer mounts lazily — it stays unmounted until the
 * user opens it for the first time, after which the standard react-query
 * cache governs revisits.
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
  const headingId = useId();
  const [historyOpen, setHistoryOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  // Set once an edit ends, so the edit button that remounts takes focus back.
  const [returnFocus, setReturnFocus] = useState(false);
  const canEdit = !editing && initial.length > 0;
  return (
    <section className={css.descriptionSection} aria-labelledby={headingId}>
      <TaskSectionHeading
        id={headingId}
        title={t('tasks.detail.sections.description')}
        trailing={
          <>
            <TaskActionButton
              icon={<History size={14} aria-hidden />}
              onClick={() => setHistoryOpen(true)}
              aria-label={t('tasks.history.title')}
            >
              {t('tasks.history.open')}
            </TaskActionButton>
            {canEdit ? (
              <TaskActionButton
                icon={<Pencil size={14} aria-hidden />}
                autoFocus={returnFocus}
                onClick={() => {
                  setEditing(true);
                }}
              >
                {t('tasks.detail.description_edit')}
              </TaskActionButton>
            ) : null}
          </>
        }
      />
      <Card className={css.descriptionCard}>
        <DescriptionEditor
          id={id}
          initial={initial}
          workspaceId={workspaceId}
          editing={editing}
          onEditingChange={(next) => {
            setReturnFocus(!next);
            setEditing(next);
          }}
        />
        <div className={css.reactions}>
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Suspense fallback={<Skeleton style={{ blockSize: '1.5rem', inlineSize: '8rem' }} />}>
            <ReactionsSection taskId={id} />
          </Suspense>
        </div>
      </Card>
      {historyOpen ? (
        <DescriptionHistoryDrawer
          taskId={id}
          currentBody={initial}
          open={historyOpen}
          onClose={() => setHistoryOpen(false)}
        />
      ) : null}
    </section>
  );
}

function ReactionsSection({ taskId }: { taskId: string }): ReactElement {
  const currentUser = useAuth(selectUser);
  const { data: reactions } = useTaskReactionsQuery(taskId);
  return (
    <ReactionBar taskId={taskId} reactions={reactions} currentUserId={currentUser?.id ?? ''} />
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
    <section className={css.commentsSection} aria-label={t('tasks.comments.title')}>
      {entries.length === 0 ? (
        <p className={cx(css.editorEnter, css.empty)}>{t('tasks.comments.empty')}</p>
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

/** Tab label of the comments tab: the title and how many comments there are. */
function CommentsTabLabel({ taskId }: { taskId: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data: comments } = useTaskCommentsQuery(taskId);
  return (
    <>
      {t('tasks.comments.title')}
      <span className={css.tabCount}>{comments.length}</span>
    </>
  );
}

/**
 * Comments, the timeline and AI activity share one tabbed block at the end
 * of the main column. The comments panel is open from the start; the other
 * two mount on first use, so their queries wait until their tab is opened.
 */
function TaskActivity({
  taskId,
  workspaceId,
  locale,
}: {
  taskId: string;
  workspaceId: string;
  locale: string;
}): ReactElement {
  const { t } = useTranslation('common');
  const { t: tAgents } = useTranslation('aiAgents');
  return (
    <ActivityTabs
      ariaLabel={t('tasks.detail.activity.title')}
      tabs={[
        {
          value: 'comments',
          label: (
            <Suspense fallback={t('tasks.comments.title')}>
              <CommentsTabLabel taskId={taskId} />
            </Suspense>
          ),
          content: (
            <Suspense fallback={<SectionSkeleton />}>
              <CommentsFeed taskId={taskId} workspaceId={workspaceId} />
            </Suspense>
          ),
        },
        {
          value: 'activity',
          label: t('tasks.detail.activity.title'),
          content: (
            <Suspense fallback={<SectionSkeleton />}>
              <TaskMiniTimeline taskId={taskId} />
            </Suspense>
          ),
        },
        {
          value: 'ai',
          label: tAgents('section.title'),
          content: (
            <Suspense fallback={<AIAgentsSkeleton />}>
              <AIAgentsSection taskId={taskId} locale={locale} />
            </Suspense>
          ),
        },
      ]}
    />
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
        <p className={css.empty}>{t('tasks.detail.assignees.empty')}</p>
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
                  className={css.assigneeRemove}
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
          <p className={css.empty}>{t('tasks.detail.assignees.none')}</p>
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
        <TaskActionButton
          icon={<Plus size={14} aria-hidden />}
          className={css.startAligned}
          onClick={() => {
            setPicking(true);
          }}
        >
          {t('tasks.detail.assignees.add')}
        </TaskActionButton>
      )}
    </>
  );
}

/** One property of the details card: a fixed label column and a value column. */
function PropertyRow({
  label,
  labelId,
  hint,
  children,
}: {
  label: string;
  labelId?: string;
  hint?: ReactElement;
  children: ReactNode;
}): ReactElement {
  return (
    <div className={css.propRow}>
      <span id={labelId} className={css.propLabel}>
        {label}
        {hint}
      </span>
      <div className={css.propValue}>{children}</div>
    </div>
  );
}

interface SidebarProps {
  id: string;
  workspaceId: string;
  state: TaskDerivedState;
  priority: TaskPriority;
  startOn: string | undefined;
  dueOn: string | undefined;
}

function Sidebar({ id, workspaceId, state, priority, startOn, dueOn }: SidebarProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const weekStart = useWeekStart();
  const { t: tAgents } = useTranslation('aiAgents');
  const { t: tLabels } = useTranslation('labels');
  const { t: tConstraints } = useTranslation('constraints');
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
        <TaskSideCardHeading title={t('tasks.detail.properties.title')} />
        <TaskSideGroup>
          <PropertyRow label={t('tasks.detail.priority_label')}>
            <PriorityPicker
              value={priority}
              onChange={(next) => {
                void handlePriorityChange(next);
              }}
            />
          </PropertyRow>
          <PropertyRow label={t('tasks.form.start')}>
            <div className={fieldStyles.dateField}>
              <DatePicker
                className={cx(fieldStyles.dateTrigger, !startOn && fieldStyles.dateEmpty)}
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
              <CalendarDays size={14} aria-hidden className={fieldStyles.dateIcon} />
            </div>
          </PropertyRow>
          <PropertyRow label={t('tasks.detail.due_label')}>
            <div className={fieldStyles.dateField}>
              <DatePicker
                className={cx(fieldStyles.dateTrigger, !dueOn && fieldStyles.dateEmpty)}
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
              <CalendarDays size={14} aria-hidden className={fieldStyles.dateIcon} />
            </div>
          </PropertyRow>
          <PropertyRow label={tLabels('title')} labelId={labelsLabelId}>
            <Suspense fallback={<TaskLabelsFieldSkeleton />}>
              <TaskLabelsField taskId={id} workspaceId={workspaceId} labelledBy={labelsLabelId} />
            </Suspense>
          </PropertyRow>
        </TaskSideGroup>

        <TaskSideGroup>
          <PropertyRow label={t('tasks.detail.assignees.title')}>
            <Suspense fallback={<SectionSkeleton />}>
              <AssigneesSection taskId={id} workspaceId={workspaceId} />
            </Suspense>
          </PropertyRow>
          <PropertyRow
            label={t('taskWorkflow.rolesHeading')}
            hint={
              <HintTip
                title={t('taskWorkflow.rolesHeading')}
                hint={t('taskWorkflow.rolesDescription')}
              />
            }
          >
            <Suspense fallback={<SectionSkeleton />}>
              <TaskRoleAssignments key={id} taskId={id} workspaceId={workspaceId} />
            </Suspense>
          </PropertyRow>
        </TaskSideGroup>
      </TaskSideCard>

      <TaskSideCard>
        <TaskSideCardHeading title={t('tasks.detail.automation.title')} />
        <AutomationTile
          icon={Bot}
          title={tAgents('task_detail.agent.assign.title')}
          hint={tAgents('task_detail.agent.assign.description')}
          summary={t('tasks.detail.automation.agent.summary')}
          body={
            <Suspense fallback={<SectionSkeleton />}>
              <AgentAssignSection taskId={id} workspaceId={workspaceId} />
            </Suspense>
          }
        />
        <AutomationTile
          icon={Lightbulb}
          title={t('tasks.detail.infer_state.title')}
          hint={t('tasks.detail.infer_state.hint')}
          marker={<AiConsequence kind="suggests" />}
          summary={t('tasks.detail.automation.next_action.summary')}
          body={
            <Suspense fallback={<SectionSkeleton />}>
              <InferStateSection taskId={id} />
            </Suspense>
          }
        />
        <AutomationTile
          icon={ListTree}
          title={t('tasks.steps.title')}
          hint={t('tasks.steps.description')}
          summary={t('tasks.detail.automation.steps.summary')}
          disclosure={{
            openLabel: t('tasks.detail.automation.steps.open'),
            tier: 'secondary',
            children: (
              <Suspense fallback={<SectionSkeleton />}>
                <TaskStepsPanel taskId={id} workspaceId={workspaceId} />
              </Suspense>
            ),
          }}
        />
        <AutomationTile
          icon={ShieldCheck}
          title={tConstraints('editor.title')}
          hint={tConstraints('editor.nlHint')}
          summary={t('tasks.detail.automation.constraints.summary')}
          status={
            <Suspense fallback={null}>
              <ConstraintCount taskId={id} />
            </Suspense>
          }
          disclosure={{
            openLabel: t('tasks.detail.automation.constraints.open'),
            closeLabel: t('tasks.detail.automation.constraints.close'),
            tier: 'tertiary',
            icon: <Pencil size={14} aria-hidden />,
            children: (
              <Suspense fallback={<SectionSkeleton />}>
                <ConstraintEditor taskId={id} />
              </Suspense>
            ),
          }}
        />
      </TaskSideCard>

      <AnalysisCard taskId={id} state={state} />
    </aside>
  );
}

/**
 * AnalysisCard holds the diagnostic views (state graph, replay, related
 * tasks). It starts collapsed and remembers the choice; its body mounts only
 * while open, so the queries behind it wait until it is asked for.
 */
function AnalysisCard({
  taskId,
  state,
}: {
  taskId: string;
  state: TaskDerivedState;
}): ReactElement {
  const { t } = useTranslation('common');
  const { collapsed, toggle } = useCollapsibleState('task-diagnostics', true);
  const bodyId = useId();
  const title = t('tasks.detail.diagnostics.title');
  return (
    <TaskSideCard>
      <TaskSideCardHeading
        title={title}
        marker={
          <TaskDisclosureToggle
            collapsed={collapsed}
            onToggle={toggle}
            controlsId={bodyId}
            label={title}
          />
        }
      />
      <Collapse open={!collapsed} id={bodyId}>
        <div className={css.analysisBody}>
          <TaskSideGroup>
            <Suspense fallback={<SectionSkeleton />}>
              <StateGraphSection taskId={taskId} current={state} />
            </Suspense>
          </TaskSideGroup>
          <TaskSideGroup>
            <Suspense fallback={<SectionSkeleton />}>
              <ReplayPanel taskId={taskId} />
            </Suspense>
          </TaskSideGroup>
          <TaskSideGroup>
            <TaskSideCardHeading
              level="sub"
              title={t('tasks.detail.duplicates.title')}
              hint={t('tasks.detail.duplicates.hint')}
            />
            <Suspense fallback={<SectionSkeleton />}>
              <RelatedTasksSection taskId={taskId} />
            </Suspense>
          </TaskSideGroup>
        </div>
      </Collapse>
    </TaskSideCard>
  );
}

/**
 * On narrow screens the sidebar lands below the whole main column, so the
 * key metadata is repeated read-only under the title.
 */
function TaskMetaStrip({
  taskId,
  priority,
  dueOn,
  locale,
}: {
  taskId: string;
  priority: TaskPriority;
  dueOn: string | undefined;
  locale: string;
}): ReactElement {
  const { t } = useTranslation('common');
  return (
    <dl className={css.metaStrip}>
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
    return <p className={css.empty}>{t('tasks.detail.duplicates.empty')}</p>;
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

/** Status line of the constraints tile: how many rules the task has. */
function ConstraintCount({ taskId }: { taskId: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data: task } = useTaskQuery(taskId);
  return (
    <span>{t('tasks.detail.automation.constraints.count', { count: task.constraintCount })}</span>
  );
}

function InferStateSection({ taskId }: { taskId: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data } = useTaskInferStateQuery(taskId);
  if (!data.proposal) {
    return <p className={css.empty}>{t('tasks.detail.infer_state.empty')}</p>;
  }
  const { transition, confidence, reason } = data.proposal;
  const transitionKey = TRANSITION_KEY[transition as TransitionName];
  const transitionLabel = transitionKey ? t(transitionKey) : transition;
  return (
    <div className={css.inferBody}>
      <div className={css.inferHead}>
        <Badge tone="accent">{transitionLabel}</Badge>
        <span className={css.score}>{confidence.toFixed(2)}</span>
      </div>
      <p className={css.inferReason}>
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
 * Tracks whether the viewport is narrower than the `sm` breakpoint, for the
 * read-only metadata strip that only the one-column layout shows.
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
    <section className={css.panel}>
      <div className={css.main}>
        <div className={css.mainHead}>
          <Suspense fallback={<Skeleton className={css.breadcrumbSkeleton} />}>
            <TaskBreadcrumb workspaceId={task.workspaceId} projectId={task.projectId} />
          </Suspense>
          {task.parentTaskId ? <TaskParentLink parentId={task.parentTaskId} /> : null}
          <TitleEditor id={id} initial={task.title} workspaceId={task.workspaceId} />
        </div>
        <TaskStatusBar
          taskId={id}
          projectId={task.projectId}
          workspaceId={task.workspaceId}
          state={state}
          title={task.title}
        />
        <div className={css.mainHead}>
          {isNarrow ? (
            <TaskMetaStrip taskId={id} priority={priority} dueOn={task.dueOn} locale={locale} />
          ) : null}
          {task.agentContext?.agent ? (
            <AgentPanel taskId={id} agentContext={task.agentContext} locale={locale} />
          ) : null}
        </div>
        <div className={css.sections}>
          <div className={css.section}>
            <DescriptionSection
              id={id}
              initial={task.description ?? ''}
              workspaceId={task.workspaceId}
            />
          </div>
          <div className={css.section}>
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
          </div>
          <div className={cx(css.section, css.relatedSection)}>
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
              <DependenciesSection taskId={id} workspaceId={task.workspaceId} />
            </Suspense>
          </div>
          <div className={cx(css.section, css.relatedSection)}>
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
              <TaskAttachments taskId={id} />
            </Suspense>
          </div>
          <div className={cx(css.section, css.relatedSection)}>
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
              <LinkedEventsSection taskId={id} workspaceId={task.workspaceId} locale={locale} />
            </Suspense>
          </div>
          <div className={cx(css.section, css.relatedSection)}>
            <TaskWorkflowPanel
              key={id}
              taskId={id}
              workspaceId={task.workspaceId}
              projectId={task.projectId}
            />
          </div>
          <div className={cx(css.section, css.relatedSection)}>
            <Suspense fallback={<SectionSkeleton />}>
              <TaskSourceLinks taskId={id} />
            </Suspense>
          </div>
          <div className={css.section}>
            <TaskActivity taskId={id} workspaceId={task.workspaceId} locale={locale} />
          </div>
        </div>
      </div>
      <Sidebar
        id={id}
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
