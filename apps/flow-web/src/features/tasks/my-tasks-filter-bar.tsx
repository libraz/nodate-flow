/**
 * MyTasksFilterBar — one toolbar row for /today: search, a Filters popover
 * (workspace, status, priority, role) and sort. A summary row of dismissible
 * chips, one per active group, appears only while a group differs from its
 * default. State lives in `use-my-task-filters`; the route re-queries on change.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Chip from '@nodate-flow/ui/primitives/chip';
import Collapse from '@nodate-flow/ui/primitives/collapse';
import Input from '@nodate-flow/ui/primitives/input';
import Popover from '@nodate-flow/ui/primitives/popover';
import Select from '@nodate-flow/ui/primitives/select';
import ToggleChip from '@nodate-flow/ui/primitives/toggle-chip';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { ListFilter } from 'lucide-react';
import { type ReactElement, type ReactNode, Suspense, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useWorkspacesQuery } from '../workspaces/api';
import {
  MY_TASK_SORTS,
  type MyTaskSort,
  TASK_ACTOR_ROLES,
  TASK_PRIORITIES,
  TASK_STATES,
  type TaskActorRole,
  type TaskDerivedState,
  type TaskPriority,
} from './api';
import { PRIORITY_KEY, STATE_KEY } from './constants';
import styles from './my-tasks-filter-bar.module.css';
import {
  DEFAULT_MY_TASK_FILTERS,
  getMyTaskFilters,
  isDefaultMyTaskFilters,
  resetMyTaskFilters,
  sameStates,
  setMyTaskFilters,
  setMyTaskSort,
  useMyTaskFilters,
  useMyTaskSort,
} from './use-my-task-filters';

const SORT_KEY: Record<MyTaskSort, string> = {
  dueOn: 'today.sort.options.due_asc',
  '-dueOn': 'today.sort.options.due_desc',
  '-priority': 'today.sort.options.priority_desc',
  priority: 'today.sort.options.priority_asc',
  '-updatedAt': 'today.sort.options.updated_desc',
  updatedAt: 'today.sort.options.updated_asc',
  '-createdAt': 'today.sort.options.created_desc',
  createdAt: 'today.sort.options.created_asc',
  title: 'today.sort.options.title_asc',
  '-title': 'today.sort.options.title_desc',
};

const ROLE_KEY: Record<TaskActorRole, string> = {
  assignee: 'today.filters.roles.assignee',
  reviewer: 'today.filters.roles.reviewer',
  approver: 'today.filters.roles.approver',
  watcher: 'today.filters.roles.watcher',
};

/** Toggle `value` in `list`, keeping the canonical `order`. */
function toggled<T>(order: readonly T[], list: readonly T[], value: T): T[] {
  const next = new Set(list);
  if (next.has(value)) next.delete(value);
  else next.add(value);
  return order.filter((item) => next.has(item));
}

function patchFilters(patch: Partial<typeof DEFAULT_MY_TASK_FILTERS>): void {
  setMyTaskFilters({ ...getMyTaskFilters(), ...patch });
}

function WorkspaceSelect({ value, label }: { value: string; label: string }): ReactElement | null {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  if (workspaces.length <= 1) return null;
  return (
    <div className={styles.section}>
      <span aria-hidden className={styles.groupLabel}>
        {label}
      </span>
      <Select
        aria-label={label}
        value={value}
        className={styles.workspaceSelect}
        onChange={(e) => {
          patchFilters({ workspaceId: e.target.value });
        }}
      >
        <option value="">{t('today.filters.workspace_all')}</option>
        {workspaces.map((w) => (
          <option key={w.id} value={w.id}>
            {w.name}
          </option>
        ))}
      </Select>
    </div>
  );
}

function WorkspaceChip({
  workspaceId,
  onDismiss,
}: {
  workspaceId: string;
  onDismiss: () => void;
}): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  const name = workspaces.find((w) => w.id === workspaceId)?.name ?? workspaceId;
  const text = t('today.filters.chip_workspace', { value: name });
  return (
    <Chip onDismiss={onDismiss} dismissLabel={t('tasks.filters.chip_remove', { label: text })}>
      {text}
    </Chip>
  );
}

function FilterSection({ label, children }: { label: string; children: ReactNode }): ReactElement {
  return (
    <div role="group" aria-label={label} className={styles.section}>
      <span aria-hidden className={styles.groupLabel}>
        {label}
      </span>
      <div className={styles.toggleGroup}>{children}</div>
    </div>
  );
}

export default function MyTasksFilterBar(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const listFormat = useMemo(
    () => new Intl.ListFormat(i18n.language, { type: 'conjunction' }),
    [i18n.language],
  );
  const filters = useMyTaskFilters();
  const sort = useMyTaskSort();

  const activeStates = !sameStates(filters.states, DEFAULT_MY_TASK_FILTERS.states);
  const activePriority = filters.priority.length > 0;
  const activeWorkspace = filters.workspaceId !== '';
  const activeRoles = filters.roles.length > 0;
  const activeCount = [activeStates, activePriority, activeWorkspace, activeRoles].filter(
    Boolean,
  ).length;
  const isDefault = isDefaultMyTaskFilters(filters);

  const searchLabel = t('tasks.filters.search_placeholder');
  const clearLabel = t('tasks.filters.clear');
  const removeLabel = (label: string): string => t('tasks.filters.chip_remove', { label });

  const statusText = t('tasks.filters.chip_status', {
    value: listFormat.format(
      TASK_STATES.filter((state) => filters.states.includes(state)).map((state) =>
        t(STATE_KEY[state]),
      ),
    ),
  });
  const priorityText = t('tasks.filters.chip_priority', {
    value: listFormat.format(
      TASK_PRIORITIES.filter((p) => filters.priority.includes(p)).map((p) => t(PRIORITY_KEY[p])),
    ),
  });
  const roleText = t('today.filters.chip_role', {
    value: listFormat.format(
      TASK_ACTOR_ROLES.filter((r) => filters.roles.includes(r)).map((r) => t(ROLE_KEY[r])),
    ),
  });

  const panel = (
    <div className={styles.panel}>
      <Suspense fallback={null}>
        <WorkspaceSelect value={filters.workspaceId} label={t('today.filters.workspace')} />
      </Suspense>
      <FilterSection label={t('tasks.filters.status')}>
        {TASK_STATES.map((state: TaskDerivedState) => (
          <ToggleChip
            key={state}
            pressed={filters.states.includes(state)}
            onPressedChange={() => {
              patchFilters({ states: toggled(TASK_STATES, filters.states, state) });
            }}
          >
            {t(STATE_KEY[state])}
          </ToggleChip>
        ))}
      </FilterSection>
      <FilterSection label={t('tasks.filters.priority')}>
        {TASK_PRIORITIES.map((p: TaskPriority) => (
          <ToggleChip
            key={p}
            pressed={filters.priority.includes(p)}
            onPressedChange={() => {
              patchFilters({ priority: toggled(TASK_PRIORITIES, filters.priority, p) });
            }}
          >
            {t(PRIORITY_KEY[p])}
          </ToggleChip>
        ))}
      </FilterSection>
      <FilterSection label={t('today.filters.role')}>
        {TASK_ACTOR_ROLES.map((role) => (
          <ToggleChip
            key={role}
            pressed={filters.roles.includes(role)}
            onPressedChange={() => {
              patchFilters({ roles: toggled(TASK_ACTOR_ROLES, filters.roles, role) });
            }}
          >
            {t(ROLE_KEY[role])}
          </ToggleChip>
        ))}
      </FilterSection>
      <div className={styles.panelFooter}>
        <Button type="button" variant="ghost" disabled={isDefault} onClick={resetMyTaskFilters}>
          {clearLabel}
        </Button>
      </div>
    </div>
  );

  return (
    <div className={styles.bar}>
      <div role="search" aria-label={t('today.title')} className={styles.toolbar}>
        <div className={styles.searchInput}>
          <Tooltip content={t('today.filters.search_hint')}>
            <Input
              type="search"
              value={filters.search}
              onChange={(e) => {
                patchFilters({ search: e.target.value });
              }}
              placeholder={searchLabel}
              aria-label={searchLabel}
            />
          </Tooltip>
        </div>
        <Popover
          placement="bottom-start"
          ariaLabel={t('today.filters.button')}
          className={styles.popoverPanel}
          content={panel}
        >
          <Button
            type="button"
            className={styles.filtersButton}
            data-active={activeCount > 0 ? '' : undefined}
            aria-label={t('today.filters.button_label', { count: activeCount })}
          >
            <ListFilter aria-hidden size="1em" />
            {t('today.filters.button')}
            {activeCount > 0 ? (
              <Badge tone="accent" aria-hidden className={styles.count}>
                {activeCount}
              </Badge>
            ) : null}
          </Button>
        </Popover>
        <div className={styles.sortField}>
          <span aria-hidden className={styles.groupLabel}>
            {t('today.sort.label')}
          </span>
          <Select
            aria-label={t('today.sort.label')}
            value={sort}
            onChange={(e) => {
              const next = MY_TASK_SORTS.find((s) => s === e.target.value);
              if (next) setMyTaskSort(next);
            }}
          >
            {MY_TASK_SORTS.map((key) => (
              <option key={key} value={key}>
                {t(SORT_KEY[key])}
              </option>
            ))}
          </Select>
        </div>
      </div>

      <Collapse open={activeCount > 0}>
        <div role="status" aria-live="polite" className={styles.chipsRow}>
          {activeWorkspace ? (
            <Suspense fallback={null}>
              <WorkspaceChip
                workspaceId={filters.workspaceId}
                onDismiss={() => {
                  patchFilters({ workspaceId: '' });
                }}
              />
            </Suspense>
          ) : null}
          {activeStates ? (
            <Chip
              onDismiss={() => {
                patchFilters({ states: DEFAULT_MY_TASK_FILTERS.states });
              }}
              dismissLabel={removeLabel(statusText)}
            >
              {statusText}
            </Chip>
          ) : null}
          {activePriority ? (
            <Chip
              onDismiss={() => {
                patchFilters({ priority: [] });
              }}
              dismissLabel={removeLabel(priorityText)}
            >
              {priorityText}
            </Chip>
          ) : null}
          {activeRoles ? (
            <Chip
              onDismiss={() => {
                patchFilters({ roles: [] });
              }}
              dismissLabel={removeLabel(roleText)}
            >
              {roleText}
            </Chip>
          ) : null}
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={resetMyTaskFilters}
            className={styles.clearButton}
          >
            {clearLabel}
          </Button>
        </div>
      </Collapse>
    </div>
  );
}
