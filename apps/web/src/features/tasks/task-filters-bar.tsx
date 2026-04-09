/**
 * TaskFiltersBar — minimal filter controls (search, status multi-select,
 * assignee picker). Plumbed through `useTaskFilters` so the active
 * board/list view re-queries on change.
 */

import Combobox from '@nodate-flow/ui/primitives/combobox';
import Input from '@nodate-flow/ui/primitives/input';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspaceUsersQuery } from '../workspaces/api';
import { TASK_STATES, type TaskDerivedState } from './api';
import {
  setTaskFilterAssignee,
  setTaskFilterSearch,
  setTaskFilterStates,
  useTaskFilters,
} from './use-task-filters';

export interface TaskFiltersBarProps {
  projectId: string;
  /**
   * Workspace the project belongs to. When provided, the Assignee filter
   * renders as a workspace-scoped user picker; when absent it falls back
   * to a disabled placeholder.
   */
  workspaceId?: string;
}

interface AssigneePickerProps {
  projectId: string;
  workspaceId: string;
  selected: string;
  label: string;
}

function AssigneePicker({
  projectId,
  workspaceId,
  selected,
  label,
}: AssigneePickerProps): ReactElement {
  const { data: users } = useWorkspaceUsersQuery(workspaceId);
  return (
    <div style={{ inlineSize: '12rem' }}>
      <Combobox
        aria-label={label}
        placeholder={label}
        value={selected}
        options={[
          { value: '', label },
          ...users.map((u) => ({ value: u.id, label: u.displayName })),
        ]}
        onChange={(v) => {
          setTaskFilterAssignee(projectId, v);
        }}
      />
    </div>
  );
}

export default function TaskFiltersBar({
  projectId,
  workspaceId,
}: TaskFiltersBarProps): ReactElement {
  const { t } = useTranslation('common');
  const filters = useTaskFilters(projectId);

  const STATE_KEY: Record<TaskDerivedState, string> = {
    open: 'tasks.status.open',
    waiting: 'tasks.status.waiting',
    review: 'tasks.status.review',
    done: 'tasks.status.done',
    cancelled: 'tasks.status.cancelled',
  };

  const selectedStates = new Set(filters.states ?? []);

  const toggleState = (state: TaskDerivedState): void => {
    const next = new Set(selectedStates);
    if (next.has(state)) next.delete(state);
    else next.add(state);
    setTaskFilterStates(projectId, [...next]);
  };

  const assigneeLabel = t('tasks.filters.assignee');

  return (
    <div
      role="search"
      aria-label={t('tasks.title')}
      style={{
        display: 'flex',
        gap: '0.75rem',
        alignItems: 'center',
        flexWrap: 'wrap',
      }}
    >
      <div style={{ inlineSize: '18rem' }}>
        <Input
          type="search"
          value={filters.search ?? ''}
          onChange={(e) => {
            setTaskFilterSearch(projectId, e.target.value);
          }}
          placeholder={t('tasks.filters.search_placeholder')}
          aria-label={t('tasks.filters.search_placeholder')}
        />
      </div>
      <div
        role="group"
        aria-label={t('tasks.filters.status')}
        style={{ display: 'inline-flex', gap: '0.375rem', flexWrap: 'wrap' }}
      >
        {TASK_STATES.map((state) => {
          const active = selectedStates.has(state);
          return (
            <button
              key={state}
              type="button"
              aria-pressed={active}
              onClick={() => {
                toggleState(state);
              }}
              style={{
                paddingBlock: '0.25rem',
                paddingInline: '0.625rem',
                borderRadius: '999px',
                border: '1px solid var(--nf-color-border)',
                background: active
                  ? 'var(--nf-color-accent-subtle, var(--nf-color-bg-sunken))'
                  : 'transparent',
                color: active
                  ? 'var(--nf-color-accent, var(--nf-color-fg))'
                  : 'var(--nf-color-fg-muted)',
                font: 'inherit',
                fontSize: '0.8125rem',
                fontWeight: active ? 600 : 400,
                cursor: 'pointer',
              }}
            >
              {t(STATE_KEY[state])}
            </button>
          );
        })}
      </div>
      {workspaceId !== undefined ? (
        <Suspense
          fallback={
            <div style={{ inlineSize: '12rem' }}>
              <Input disabled placeholder={assigneeLabel} aria-label={assigneeLabel} />
            </div>
          }
        >
          <AssigneePicker
            projectId={projectId}
            workspaceId={workspaceId}
            selected={filters.assigneeId ?? ''}
            label={assigneeLabel}
          />
        </Suspense>
      ) : (
        <div style={{ inlineSize: '12rem' }}>
          <Input disabled placeholder={assigneeLabel} aria-label={assigneeLabel} />
        </div>
      )}
    </div>
  );
}
