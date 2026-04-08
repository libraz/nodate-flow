/**
 * /tasks/$taskId — task detail panel.
 *
 * Two-column layout: main content (title, description, comments) on the
 * left, sidebar (state, priority, due, assignees, transitions) on the
 * right. State transitions go through `useTransitionTask`; description /
 * title / priority / due edits go through `useUpdateTask`.
 *
 * KNOWN GAP: the Task DTO does not expose `workspaceId`, and the Project
 * DTO doesn't either. The assignee picker is therefore gated as a TODO
 * and disabled — we have no workspace to source members from. Once
 * workspaceId surfaces on Task (or Project), the combobox below can be
 * wired to `useWorkspaceMembersQuery`.
 */

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import Separator from '@nodate-flow/ui/primitives/separator';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { createFileRoute } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  TRANSITIONS_BY_STATE,
  type TaskDerivedState,
  type TaskPriority,
  type TransitionName,
  useAddTaskActor,
  useAddTaskComment,
  useRemoveTaskActor,
  useTaskActorsQuery,
  useTaskCommentsQuery,
  useTaskQuery,
  useTransitionTask,
  useUpdateTask,
} from '../features/tasks/api';
import { useWorkspaceMembersQuery } from '../features/workspaces/api';

const PRIORITY_KEY: Record<TaskPriority, string> = {
  0: 'tasks.priority.none',
  1: 'tasks.priority.low',
  2: 'tasks.priority.medium',
  3: 'tasks.priority.high',
  4: 'tasks.priority.urgent',
};

const STATE_KEY: Record<TaskDerivedState, string> = {
  open: 'tasks.status.open',
  waiting: 'tasks.status.waiting',
  review: 'tasks.status.review',
  done: 'tasks.status.done',
  cancelled: 'tasks.status.cancelled',
};

const STATE_TONE: Record<TaskDerivedState, BadgeTone> = {
  open: 'info',
  waiting: 'warning',
  review: 'accent',
  done: 'success',
  cancelled: 'neutral',
};

const TRANSITION_KEY: Record<TransitionName, string> = {
  start: 'tasks.detail.transitions.start',
  block: 'tasks.detail.transitions.block',
  unblock: 'tasks.detail.transitions.unblock',
  submit: 'tasks.detail.transitions.submit',
  complete: 'tasks.detail.transitions.complete',
  reopen: 'tasks.detail.transitions.reopen',
  cancel: 'tasks.detail.transitions.cancel',
};

const PRIORITIES: readonly TaskPriority[] = [0, 1, 2, 3, 4];

function formatDate(iso: string, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(
      new Date(iso),
    );
  } catch {
    return iso;
  }
}

interface TaskDetailPanelProps {
  id: string;
}

function TitleEditor({ id, initial }: { id: string; initial: string }): ReactElement {
  const { t } = useTranslation('common');
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(initial);
  const update = useUpdateTask();

  if (!editing) {
    return (
      <button
        type="button"
        onClick={() => {
          setValue(initial);
          setEditing(true);
        }}
        aria-label={t('tasks.detail.title_edit')}
        style={{
          background: 'none',
          border: 'none',
          padding: 0,
          cursor: 'pointer',
          color: 'var(--color-fg)',
          font: 'inherit',
          fontFamily: 'var(--font-display)',
          fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
          textAlign: 'start',
        }}
      >
        {initial}
      </button>
    );
  }

  const handleSave = async (): Promise<void> => {
    const next = value.trim();
    if (next.length === 0 || next === initial) {
      setEditing(false);
      return;
    }
    try {
      await update.mutateAsync({ id, patch: { title: next } });
      setEditing(false);
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.errors.update_failed') });
    }
  };

  return (
    <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
      <Input
        value={value}
        onChange={(e) => {
          setValue(e.target.value);
        }}
        autoFocus
        aria-label={t('tasks.detail.title_edit')}
        style={{ flex: 1 }}
      />
      <Button
        type="button"
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
        }}
      >
        {t('tasks.detail.cancel')}
      </Button>
    </div>
  );
}

function DescriptionEditor({
  id,
  initial,
}: {
  id: string;
  initial: string;
}): ReactElement {
  const { t } = useTranslation('common');
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(initial);
  const update = useUpdateTask();

  if (!editing) {
    return (
      <button
        type="button"
        onClick={() => {
          setValue(initial);
          setEditing(true);
        }}
        aria-label={t('tasks.detail.description_edit')}
        style={{
          background: 'none',
          border: 'none',
          padding: 0,
          cursor: 'pointer',
          color: initial.length > 0 ? 'var(--color-fg)' : 'var(--color-muted)',
          font: 'inherit',
          textAlign: 'start',
          whiteSpace: 'pre-wrap',
          minBlockSize: '3rem',
          inlineSize: '100%',
        }}
      >
        {initial.length > 0 ? initial : t('tasks.detail.description_empty')}
      </button>
    );
  }

  const handleSave = async (): Promise<void> => {
    try {
      await update.mutateAsync({ id, patch: { description: value } });
      setEditing(false);
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.errors.update_failed') });
    }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
      <Textarea
        value={value}
        onChange={(e) => {
          setValue(e.target.value);
        }}
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

function CommentsFeed({ taskId }: { taskId: string }): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: comments } = useTaskCommentsQuery(taskId);
  const add = useAddTaskComment();
  const [body, setBody] = useState('');
  const locale = i18n.resolvedLanguage ?? 'en';

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const trimmed = body.trim();
    if (trimmed.length === 0) return;
    try {
      await add.mutateAsync({ taskId, body: trimmed });
      setBody('');
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.errors.comment_add_failed') });
    }
  };

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <h2 style={{ margin: 0, fontSize: '1.125rem' }}>{t('tasks.comments.title')}</h2>
      {comments.length === 0 ? (
        <p style={{ color: 'var(--color-muted)', margin: 0 }}>{t('tasks.comments.empty')}</p>
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
              <Card style={{ padding: '0.875rem 1rem' }}>
                <header
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    gap: '0.5rem',
                    marginBlockEnd: '0.375rem',
                  }}
                >
                  <strong>{c.authorDisplayName}</strong>
                  <span
                    style={{
                      color: 'var(--color-muted)',
                      fontVariantNumeric: 'tabular-nums',
                      fontSize: '0.875rem',
                    }}
                  >
                    {formatDate(c.createdAt, locale)}
                  </span>
                </header>
                <p style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{c.body}</p>
              </Card>
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
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.errors.update_failed') });
    }
  };

  const handleRemove = async (actorId: string): Promise<void> => {
    try {
      await removeActor.mutateAsync({ taskId, actorId });
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.errors.update_failed') });
    }
  };

  return (
    <>
      {assignees.length === 0 ? (
        <p style={{ margin: 0, color: 'var(--color-muted)' }}>
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
          <p style={{ margin: 0, color: 'var(--color-muted)' }}>
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
  dueOn: string | undefined;
}

function Sidebar({
  id,
  projectId,
  workspaceId,
  state,
  priority,
  dueOn,
}: SidebarProps): ReactElement {
  const { t } = useTranslation('common');
  const update = useUpdateTask();
  const transition = useTransitionTask();

  const handlePriorityChange = async (next: TaskPriority): Promise<void> => {
    try {
      await update.mutateAsync({ id, patch: { priority: next } });
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.errors.update_failed') });
    }
  };

  const handleDueChange = async (next: string): Promise<void> => {
    try {
      await update.mutateAsync({ id, patch: { dueOn: next } });
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.errors.update_failed') });
    }
  };

  const handleTransition = (name: TransitionName): void => {
    transition.mutate(
      { id, transition: name, projectId },
      {
        onError: () => {
          toaster.show({ tone: 'warning', message: t('tasks.errors.illegal_transition') });
        },
      },
    );
  };

  const legal = TRANSITIONS_BY_STATE[state];

  return (
    <aside style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={{ color: 'var(--color-muted)' }}>{t('tasks.detail.state_label')}</span>
          <Badge tone={STATE_TONE[state]}>{t(STATE_KEY[state])}</Badge>
        </div>
        <Separator />
        <FormField label={t('tasks.detail.priority_label')}>
          {(control) => (
            <Select
              {...control}
              value={String(priority)}
              onChange={(e) => {
                const next = Number.parseInt(e.target.value, 10) as TaskPriority;
                void handlePriorityChange(next);
              }}
            >
              {PRIORITIES.map((p) => (
                <option key={p} value={String(p)}>
                  {t(PRIORITY_KEY[p])}
                </option>
              ))}
            </Select>
          )}
        </FormField>
        <FormField label={t('tasks.detail.due_label')}>
          {(control) => (
            <Input
              {...control}
              type="date"
              value={dueOn ?? ''}
              onChange={(e) => {
                void handleDueChange(e.target.value);
              }}
            />
          )}
        </FormField>
      </Card>

      <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h2 style={{ margin: 0, fontSize: '1rem' }}>{t('tasks.detail.transitions.title')}</h2>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.5rem' }}>
          {legal.map((name) => (
            <Button
              key={name}
              type="button"
              onClick={() => {
                handleTransition(name);
              }}
            >
              {t(TRANSITION_KEY[name])}
            </Button>
          ))}
        </div>
      </Card>

      <Card style={{ padding: '1rem', display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h2 style={{ margin: 0, fontSize: '1rem' }}>{t('tasks.detail.assignees.title')}</h2>
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
    </aside>
  );
}

function TaskDetailPanel({ id }: TaskDetailPanelProps): ReactElement {
  const { data: task } = useTaskQuery(id);
  const state = task.derivedState as TaskDerivedState;
  const priority = (task.priority as TaskPriority) ?? 0;

  return (
    <section
      style={{
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
        display: 'grid',
        gridTemplateColumns: 'minmax(0, 1fr) minmax(16rem, 22rem)',
        gap: '1.5rem',
      }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem', minInlineSize: 0 }}>
        <TitleEditor id={id} initial={task.title} />
        <Card style={{ padding: '1rem' }}>
          <DescriptionEditor id={id} initial={task.description ?? ''} />
        </Card>
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
        dueOn={task.dueOn}
      />
    </section>
  );
}

function TaskDetailRoute(): ReactElement {
  const { taskId } = Route.useParams();
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

export const Route = createFileRoute('/_authenticated/tasks/$taskId')({
  component: TaskDetailRoute,
});
