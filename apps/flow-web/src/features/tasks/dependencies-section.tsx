/**
 * DependenciesSection — task detail "Dependencies" panel.
 *
 * Renders three groups (Blocked by / Blocks / Relates to) backed by
 * `GET /tasks/{id}/dependencies`. The picker uses `GET /tasks?q=...`
 * scoped to the current workspace; results are debounced client-side.
 */

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link } from '@tanstack/react-router';
import { type ReactElement, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  type TaskDependencyEdge,
  type TaskDependencyKind,
  type TaskDerivedState,
  useAddTaskDependency,
  useRemoveTaskDependency,
  useTaskDependenciesQuery,
  useTaskSearch,
} from './api';
import { STATE_TONE } from './constants';

interface DependenciesSectionProps {
  taskId: string;
  workspaceId: string;
}

type AddableKind = Extract<TaskDependencyKind, 'blocks' | 'relates'>;

/**
 * DependenciesSection renders incoming/outgoing dependency edges and
 * an inline picker to add new ones.
 */
export default function DependenciesSection({
  taskId,
  workspaceId,
}: DependenciesSectionProps): ReactElement {
  const { t } = useTranslation('common');
  const { data } = useTaskDependenciesQuery(taskId);
  const remove = useRemoveTaskDependency();
  const [picking, setPicking] = useState(false);

  const blocks = useMemo(() => data.outgoing.filter((e) => e.kind === 'blocks'), [data.outgoing]);
  const blockedBy = useMemo(
    () => data.incoming.filter((e) => e.kind === 'blocks'),
    [data.incoming],
  );
  const relates = useMemo(
    () => [
      ...data.outgoing.filter((e) => e.kind === 'relates'),
      ...data.incoming.filter((e) => e.kind === 'relates'),
    ],
    [data.outgoing, data.incoming],
  );

  const isEmpty = blocks.length === 0 && blockedBy.length === 0 && relates.length === 0;

  const handleRemove = async (depId: string): Promise<void> => {
    try {
      await remove.mutateAsync({ taskId, depId });
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.detail.dependencies.removeError') });
    }
  };

  return (
    <section
      aria-label={t('tasks.detail.dependencies.title')}
      style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}
    >
      <header style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <h2 style={{ margin: 0, fontSize: '1.125rem' }}>{t('tasks.detail.dependencies.title')}</h2>
        {!picking ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              setPicking(true);
            }}
          >
            {t('tasks.detail.dependencies.add')}
          </Button>
        ) : null}
      </header>

      {picking ? (
        <DependencyPicker
          taskId={taskId}
          workspaceId={workspaceId}
          excludeIds={
            new Set([
              taskId,
              ...data.outgoing.map((e) => e.otherTaskId),
              ...data.incoming.map((e) => e.otherTaskId),
            ])
          }
          onClose={() => {
            setPicking(false);
          }}
        />
      ) : null}

      {isEmpty && !picking ? (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
          {t('tasks.detail.dependencies.empty')}
        </p>
      ) : null}

      <DependencyGroup
        label={t('tasks.detail.dependencies.blockedBy')}
        items={blockedBy}
        tone="danger"
        onRemove={handleRemove}
      />
      <DependencyGroup
        label={t('tasks.detail.dependencies.blocks')}
        items={blocks}
        tone="warning"
        onRemove={handleRemove}
      />
      <DependencyGroup
        label={t('tasks.detail.dependencies.relates')}
        items={relates}
        tone="neutral"
        onRemove={handleRemove}
      />
    </section>
  );
}

function DependencyGroup({
  label,
  items,
  tone,
  onRemove,
}: {
  label: string;
  items: TaskDependencyEdge[];
  tone: BadgeTone;
  onRemove: (depId: string) => void | Promise<void>;
}): ReactElement | null {
  const { t } = useTranslation('common');
  if (items.length === 0) return null;
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.375rem' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: '0.5rem',
          fontSize: '0.75rem',
          textTransform: 'uppercase',
          letterSpacing: '0.05em',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        <Badge tone={tone}>{label}</Badge>
        <span>({items.length})</span>
      </div>
      <ul
        style={{
          listStyle: 'none',
          margin: 0,
          padding: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: '0.25rem',
        }}
      >
        {items.map((edge) => {
          const stateTone = STATE_TONE[edge.otherTaskDerivedState as TaskDerivedState] ?? 'neutral';
          return (
            <li
              key={edge.id}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '0.5rem',
                padding: '0.5rem 0.625rem',
                borderRadius: '0.5rem',
                background: 'var(--nf-color-surface))',
              }}
            >
              <Badge tone={stateTone}>{edge.otherTaskDerivedState}</Badge>
              <Link
                to="/tasks/$taskId"
                params={{ taskId: edge.otherTaskId }}
                style={{
                  flex: 1,
                  minWidth: 0,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  color: 'inherit',
                  textDecoration: 'none',
                }}
              >
                {edge.otherTaskTitle}
              </Link>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                aria-label={t('tasks.detail.dependencies.removeNamed', {
                  title: edge.otherTaskTitle,
                })}
                onClick={() => {
                  void onRemove(edge.id);
                }}
              >
                ×
              </Button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function DependencyPicker({
  taskId,
  workspaceId,
  excludeIds,
  onClose,
}: {
  taskId: string;
  workspaceId: string;
  excludeIds: Set<string>;
  onClose: () => void;
}): ReactElement {
  const { t } = useTranslation('common');
  const [kind, setKind] = useState<AddableKind>('blocks');
  const [input, setInput] = useState('');
  const [debounced, setDebounced] = useState('');
  const add = useAddTaskDependency();

  useEffect(() => {
    const id = window.setTimeout(() => {
      setDebounced(input.trim());
    }, 200);
    return () => {
      window.clearTimeout(id);
    };
  }, [input]);

  const search = useTaskSearch(workspaceId, debounced, debounced.length > 0);
  const results = (search.data ?? []).filter((task) => !excludeIds.has(task.id));

  const handlePick = async (toTaskId: string): Promise<void> => {
    try {
      await add.mutateAsync({ taskId, toTaskId, kind });
      onClose();
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.detail.dependencies.addError') });
    }
  };

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '0.5rem',
        padding: '0.75rem',
        borderRadius: '0.5rem',
        border: '1px solid var(--nf-color-border))',
      }}
    >
      <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
        <Select
          aria-label={t('tasks.detail.dependencies.kindLabel')}
          value={kind}
          onChange={(e) => {
            setKind(e.target.value as AddableKind);
          }}
          style={{ inlineSize: '12rem' }}
        >
          <option value="blocks">{t('tasks.detail.dependencies.kind.blocks')}</option>
          <option value="relates">{t('tasks.detail.dependencies.kind.relates')}</option>
        </Select>
        <Input
          value={input}
          onChange={(e) => {
            setInput(e.target.value);
          }}
          placeholder={t('tasks.detail.dependencies.search')}
          autoFocus
          aria-label={t('tasks.detail.dependencies.search')}
          style={{ flex: 1 }}
        />
        <Button type="button" variant="ghost" size="sm" onClick={onClose}>
          {t('tasks.detail.dependencies.addCancel')}
        </Button>
      </div>
      {debounced.length === 0 ? (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.8125rem' }}>
          {t('tasks.detail.dependencies.searchHint')}
        </p>
      ) : search.isFetching ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: '0.5rem' }}>
          <Spinner label={t('common.loading')} />
        </div>
      ) : results.length === 0 ? (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.8125rem' }}>
          {t('tasks.detail.dependencies.searchEmpty')}
        </p>
      ) : (
        <ul
          style={{
            listStyle: 'none',
            margin: 0,
            padding: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: '0.25rem',
            maxBlockSize: '16rem',
            overflowY: 'auto',
          }}
        >
          {results.map((task) => (
            <li key={task.id}>
              <button
                type="button"
                onClick={() => {
                  void handlePick(task.id);
                }}
                style={{
                  inlineSize: '100%',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '0.5rem',
                  padding: '0.5rem 0.625rem',
                  borderRadius: '0.375rem',
                  background: 'transparent',
                  border: '1px solid transparent',
                  textAlign: 'start',
                  cursor: 'pointer',
                  color: 'inherit',
                  font: 'inherit',
                }}
              >
                <Badge tone={STATE_TONE[task.derivedState as TaskDerivedState] ?? 'neutral'}>
                  {task.derivedState}
                </Badge>
                <span
                  style={{
                    flex: 1,
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {task.title}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
