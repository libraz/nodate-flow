/**
 * TaskFiltersBar — filter controls (search, status multi-select, priority
 * multi-select, assignee picker) plus a row of dismissible active filter
 * chips. Plumbed through `useTaskFilters` so the active board/list view
 * re-queries on change.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Chip from '@nodate-flow/ui/primitives/chip';
import Collapse from '@nodate-flow/ui/primitives/collapse';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import Input from '@nodate-flow/ui/primitives/input';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import ToggleChip from '@nodate-flow/ui/primitives/toggle-chip';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';
import { useWorkspaceUsersQuery } from '../workspaces/api';
import { TASK_PRIORITIES, TASK_STATES, type TaskDerivedState, type TaskPriority } from './api';
import { PRIORITY_KEY, STATE_KEY } from './constants';
import LensPicker from './lens-picker';
import styles from './task-filters-bar.module.css';
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
    <div className={styles.assigneePicker}>
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

/** Active assignee chip; resolves the display name. */
function AssigneeChip({
  workspaceId,
  assigneeId,
  onDismiss,
}: {
  workspaceId: string;
  assigneeId: string;
  onDismiss: () => void;
}): ReactElement {
  const { t } = useTranslation('common');
  const { data: users } = useWorkspaceUsersQuery(workspaceId);
  const user = users.find((u) => u.id === assigneeId);
  const text = t('tasks.filters.chip_assignee', { value: user?.displayName ?? assigneeId });
  return (
    <Chip onDismiss={onDismiss} dismissLabel={t('tasks.filters.chip_remove', { label: text })}>
      {text}
    </Chip>
  );
}

export default function TaskFiltersBar({
  projectId,
  workspaceId,
}: TaskFiltersBarProps): ReactElement {
  const { t } = useTranslation('common');
  const filters = useTaskFilters(projectId);

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
  const assigneeChipText = t('tasks.filters.chip_assignee', { value: filters.assigneeId });
  const searchChipText = t('tasks.filters.chip_search', { value: filters.search });
  const hasActiveFilter = activeSearch || activeStates || activeAssignee || activePriority;

  return (
    <div className={styles.bar}>
      {/* Controls row */}
      <div role="search" aria-label={t('tasks.title')} className={styles.controlsRow}>
        <div className={styles.searchInput}>
          <Tooltip content={t('tasks.filters.search_hint')}>
            <Input
              type="search"
              value={filters.search ?? ''}
              onChange={(e) => {
                setTaskFilterSearch(projectId, e.target.value);
              }}
              placeholder={t('tasks.filters.search_placeholder')}
              aria-label={t('tasks.filters.search_placeholder')}
            />
          </Tooltip>
        </div>
        <div role="group" aria-label={t('tasks.filters.status')} className={styles.filterGroup}>
          <span aria-hidden className={styles.groupLabel}>
            {t('tasks.filters.status')}
          </span>
          <div className={styles.toggleGroup}>
            {TASK_STATES.map((state) => {
              const active = selectedStates.has(state);
              return (
                <ToggleChip
                  key={state}
                  pressed={active}
                  onPressedChange={() => {
                    toggleState(state);
                  }}
                >
                  {t(STATE_KEY[state])}
                </ToggleChip>
              );
            })}
          </div>
        </div>
        <div role="group" aria-label={t('tasks.filters.priority')} className={styles.filterGroup}>
          <span aria-hidden className={styles.groupLabel}>
            {t('tasks.filters.priority')}
          </span>
          <div className={styles.toggleGroup}>
            {TASK_PRIORITIES.map((p) => {
              const active = selectedPriorities.has(p);
              return (
                <ToggleChip
                  key={p}
                  pressed={active}
                  onPressedChange={() => {
                    togglePriority(p);
                  }}
                >
                  {t(PRIORITY_KEY[p])}
                </ToggleChip>
              );
            })}
          </div>
        </div>
        {workspaceId !== undefined ? (
          <Suspense
            fallback={
              <div className={styles.assigneePicker}>
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
          <div className={styles.assigneePicker}>
            <Input disabled placeholder={assigneeLabel} aria-label={assigneeLabel} />
          </div>
        )}
        {workspaceId !== undefined ? (
          <Suspense fallback={<Skeleton className={styles.lensSkeleton} />}>
            <LensPicker workspaceId={workspaceId} projectId={projectId} />
          </Suspense>
        ) : null}
        {hasActiveFilter ? (
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              resetTaskFilters(projectId);
            }}
            className={styles.clearButton}
          >
            {t('tasks.filters.clear')}
          </Button>
        ) : null}
      </div>

      {/* Active filter chips */}
      <Collapse open={hasActiveFilter}>
        <div role="status" aria-live="polite" className={styles.chipsRow}>
          {activeSearch ? (
            <Chip
              onDismiss={() => {
                setTaskFilterSearch(projectId, '');
              }}
              dismissLabel={t('tasks.filters.chip_remove', { label: searchChipText })}
            >
              {searchChipText}
            </Chip>
          ) : null}
          {activeStates
            ? (filters.states ?? []).map((state) => {
                const text = t('tasks.filters.chip_status', { value: t(STATE_KEY[state]) });
                return (
                  <Chip
                    key={`state-${state}`}
                    onDismiss={() => {
                      const next = (filters.states ?? []).filter((s) => s !== state);
                      setTaskFilterStates(projectId, next);
                    }}
                    dismissLabel={t('tasks.filters.chip_remove', { label: text })}
                  >
                    {text}
                  </Chip>
                );
              })
            : null}
          {activePriority
            ? (filters.priority ?? []).map((p) => {
                const text = t('tasks.filters.chip_priority', { value: t(PRIORITY_KEY[p]) });
                return (
                  <Chip
                    key={`priority-${String(p)}`}
                    onDismiss={() => {
                      const next = (filters.priority ?? []).filter((v) => v !== p);
                      setTaskFilterPriority(projectId, next);
                    }}
                    dismissLabel={t('tasks.filters.chip_remove', { label: text })}
                  >
                    {text}
                  </Chip>
                );
              })
            : null}
          {activeAssignee ? (
            workspaceId !== undefined ? (
              <Suspense fallback={null}>
                <AssigneeChip
                  workspaceId={workspaceId}
                  assigneeId={filters.assigneeId ?? ''}
                  onDismiss={() => {
                    setTaskFilterAssignee(projectId, '');
                  }}
                />
              </Suspense>
            ) : (
              <Chip
                onDismiss={() => {
                  setTaskFilterAssignee(projectId, '');
                }}
                dismissLabel={t('tasks.filters.chip_remove', { label: assigneeChipText })}
              >
                {assigneeChipText}
              </Chip>
            )
          ) : null}
        </div>
      </Collapse>
    </div>
  );
}
