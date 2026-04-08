/**
 * TaskFiltersBar — minimal filter controls (search, status multi-select,
 * assignee id input). Plumbed through `useTaskFilters` so the active
 * board/list view re-queries on change.
 */

import Input from '@nodate-flow/ui/primitives/input';
import type { ChangeEvent, ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { TASK_STATES, type TaskDerivedState } from './api';
import {
  setTaskFilterAssignee,
  setTaskFilterSearch,
  setTaskFilterStates,
  useTaskFilters,
} from './use-task-filters';

export interface TaskFiltersBarProps {
  projectId: string;
}

const STATE_KEY: Record<TaskDerivedState, string> = {
  open: 'tasks.status.open',
  waiting: 'tasks.status.waiting',
  review: 'tasks.status.review',
  done: 'tasks.status.done',
  cancelled: 'tasks.status.cancelled',
};

export default function TaskFiltersBar({ projectId }: TaskFiltersBarProps): ReactElement {
  const { t } = useTranslation('common');
  const filters = useTaskFilters(projectId);

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
        style={{ minInlineSize: '12rem', minBlockSize: '2.25rem' }}
      >
        {TASK_STATES.map((state) => (
          <option key={state} value={state}>
            {t(STATE_KEY[state])}
          </option>
        ))}
      </select>
      <Input
        value={filters.assigneeId ?? ''}
        onChange={(e) => {
          setTaskFilterAssignee(projectId, e.target.value);
        }}
        placeholder={t('tasks.filters.assignee')}
        aria-label={t('tasks.filters.assignee')}
        style={{ minInlineSize: '14rem' }}
      />
    </div>
  );
}
