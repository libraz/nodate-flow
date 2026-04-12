/**
 * TaskFiltersBar — filter controls (search, status multi-select, priority
 * multi-select, assignee picker) plus a row of dismissible active filter
 * chips. Plumbed through `useTaskFilters` so the active board/list view
 * re-queries on change.
 */

import Chip from '@nodate-flow/ui/primitives/chip';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import Input from '@nodate-flow/ui/primitives/input';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspaceUsersQuery } from '../workspaces/api';
import {
  PRIORITY_I18N_KEY,
  TASK_PRIORITIES,
  TASK_STATES,
  type TaskDerivedState,
  type TaskPriority,
} from './api';
import {
  resetTaskFilters,
  setTaskFilterAssignee,
  setTaskFilterPriority,
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

/** Resolve the assignee display name for an active chip. */
function AssigneeChipLabel({
  workspaceId,
  assigneeId,
}: {
  workspaceId: string;
  assigneeId: string;
}): ReactElement {
  const { t } = useTranslation('common');
  const { data: users } = useWorkspaceUsersQuery(workspaceId);
  const user = users.find((u) => u.id === assigneeId);
  const name = user?.displayName ?? assigneeId;
  return <>{t('tasks.filters.chip_assignee', { value: name })}</>;
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
  const selectedPriorities = new Set(filters.priority ?? []);

  const toggleState = (state: TaskDerivedState): void => {
    const next = new Set(selectedStates);
    if (next.has(state)) next.delete(state);
    else next.add(state);
    setTaskFilterStates(projectId, [...next]);
  };

  const togglePriority = (p: TaskPriority): void => {
    const next = new Set(selectedPriorities);
    if (next.has(p)) next.delete(p);
    else next.add(p);
    setTaskFilterPriority(projectId, [...next]);
  };

  const assigneeLabel = t('tasks.filters.assignee');
  const activeSearch = filters.search != null && filters.search.length > 0;
  const activeStates = filters.states != null && filters.states.length > 0;
  const activeAssignee = filters.assigneeId != null && filters.assigneeId.length > 0;
  const activePriority = filters.priority != null && filters.priority.length > 0;
  const hasActiveFilter = activeSearch || activeStates || activeAssignee || activePriority;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
      {/* Controls row */}
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
        <div
          role="group"
          aria-label={t('tasks.filters.priority')}
          style={{ display: 'inline-flex', gap: '0.375rem', flexWrap: 'wrap' }}
        >
          {TASK_PRIORITIES.map((p) => {
            const active = selectedPriorities.has(p);
            return (
              <button
                key={p}
                type="button"
                aria-pressed={active}
                onClick={() => {
                  togglePriority(p);
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
                {t(PRIORITY_I18N_KEY[p])}
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
        {hasActiveFilter ? (
          <button
            type="button"
            onClick={() => {
              resetTaskFilters(projectId);
            }}
            style={{
              marginInlineStart: 'auto',
              background: 'none',
              border: 'none',
              padding: '0.25rem 0.5rem',
              color: 'var(--nf-color-fg-muted)',
              cursor: 'pointer',
              font: 'inherit',
              fontSize: '0.8125rem',
              textDecoration: 'underline',
            }}
          >
            {t('tasks.filters.clear')}
          </button>
        ) : null}
      </div>

      {/* Active filter chips */}
      {hasActiveFilter ? (
        <div
          role="status"
          aria-live="polite"
          style={{
            display: 'flex',
            gap: '0.375rem',
            flexWrap: 'wrap',
            alignItems: 'center',
          }}
        >
          {activeSearch ? (
            <Chip
              onDismiss={() => {
                setTaskFilterSearch(projectId, '');
              }}
            >
              {t('tasks.filters.chip_search', { value: filters.search })}
            </Chip>
          ) : null}
          {activeStates
            ? (filters.states ?? []).map((state) => (
                <Chip
                  key={`state-${state}`}
                  onDismiss={() => {
                    const next = (filters.states ?? []).filter((s) => s !== state);
                    setTaskFilterStates(projectId, next);
                  }}
                >
                  {t('tasks.filters.chip_status', { value: t(STATE_KEY[state]) })}
                </Chip>
              ))
            : null}
          {activePriority
            ? (filters.priority ?? []).map((p) => (
                <Chip
                  key={`priority-${String(p)}`}
                  onDismiss={() => {
                    const next = (filters.priority ?? []).filter((v) => v !== p);
                    setTaskFilterPriority(projectId, next);
                  }}
                >
                  {t('tasks.filters.chip_priority', { value: t(PRIORITY_I18N_KEY[p]) })}
                </Chip>
              ))
            : null}
          {activeAssignee ? (
            workspaceId !== undefined ? (
              <Suspense fallback={null}>
                <Chip
                  onDismiss={() => {
                    setTaskFilterAssignee(projectId, '');
                  }}
                >
                  <AssigneeChipLabel
                    workspaceId={workspaceId}
                    assigneeId={filters.assigneeId ?? ''}
                  />
                </Chip>
              </Suspense>
            ) : (
              <Chip
                onDismiss={() => {
                  setTaskFilterAssignee(projectId, '');
                }}
              >
                {t('tasks.filters.chip_assignee', { value: filters.assigneeId })}
              </Chip>
            )
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
