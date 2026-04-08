/**
 * TaskFiltersBar — minimal filter controls (search, status multi-select,
 * assignee picker). Plumbed through `useTaskFilters` so the active
 * board/list view re-queries on change.
 */

import Combobox from '@nodate-flow/ui/primitives/combobox';
import Input from '@nodate-flow/ui/primitives/input';
import { type ChangeEvent, type ReactElement, Suspense } from 'react';
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
    <Combobox
      aria-label={label}
      placeholder={label}
      value={selected}
      options={[{ value: '', label }, ...users.map((u) => ({ value: u.id, label: u.displayName }))]}
      onChange={(v) => {
        setTaskFilterAssignee(projectId, v);
      }}
    />
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

  const handleStatesChange = (e: ChangeEvent<HTMLSelectElement>): void => {
    const next: TaskDerivedState[] = [];
    for (const opt of e.target.selectedOptions) {
      if (
        opt.value === 'open' ||
        opt.value === 'waiting' ||
        opt.value === 'review' ||
        opt.value === 'done' ||
        opt.value === 'cancelled'
      ) {
        next.push(opt.value);
      }
    }
    setTaskFilterStates(projectId, next);
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
      <Input
        type="search"
        value={filters.search ?? ''}
        onChange={(e) => {
          setTaskFilterSearch(projectId, e.target.value);
        }}
        placeholder={t('tasks.filters.search_placeholder')}
        aria-label={t('tasks.filters.search_placeholder')}
        style={{ minInlineSize: '14rem' }}
      />
      <select
        multiple
        value={[...(filters.states ?? [])]}
        onChange={handleStatesChange}
        aria-label={t('tasks.filters.status')}
        style={{
          minInlineSize: '12rem',
          minBlockSize: '8rem',
          padding: '0.25rem',
          borderRadius: '0.25rem',
          border: '1px solid var(--color-border)',
          background: 'var(--color-bg)',
          color: 'var(--color-fg)',
        }}
      >
        {TASK_STATES.map((state) => (
          <option key={state} value={state}>
            {t(STATE_KEY[state])}
          </option>
        ))}
      </select>
      {workspaceId !== undefined ? (
        <Suspense
          fallback={<Input disabled placeholder={assigneeLabel} aria-label={assigneeLabel} />}
        >
          <AssigneePicker
            projectId={projectId}
            workspaceId={workspaceId}
            selected={filters.assigneeId ?? ''}
            label={assigneeLabel}
          />
        </Suspense>
      ) : (
        <Input disabled placeholder={assigneeLabel} aria-label={assigneeLabel} />
      )}
    </div>
  );
}
