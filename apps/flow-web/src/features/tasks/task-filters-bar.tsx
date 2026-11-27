/**
 * TaskFiltersBar — the project tasks toolbar: search, a Filters popover
 * (status, priority, assignee), saved views, then the caller's `actions`
 * (view switcher, New task) at the end of the same row. A row of
 * dismissible chips, one per active facet, appears only while filtering.
 * State lives in `useTaskFilters`, so the active view re-queries on change.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Chip from '@nodate-flow/ui/primitives/chip';
import Collapse from '@nodate-flow/ui/primitives/collapse';
import Input from '@nodate-flow/ui/primitives/input';
import Popover from '@nodate-flow/ui/primitives/popover';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import ToggleChip from '@nodate-flow/ui/primitives/toggle-chip';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { ListFilter } from 'lucide-react';
import { type ReactElement, type ReactNode, Suspense } from 'react';
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
   * Workspace the project belongs to. Enables the assignee facet and saved
   * views; without it the assignee facet is omitted.
   */
  workspaceId?: string;
  /** Trailing controls on the same row, e.g. the view switcher and New task. */
  actions?: ReactNode;
}

/** Toggle `value` in `list`, keeping the canonical `order`. */
function toggled<T>(order: readonly T[], list: readonly T[], value: T): T[] {
  const next = new Set(list);
  if (next.has(value)) next.delete(value);
  else next.add(value);
  return order.filter((item) => next.has(item));
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

/** Single-select member facet: pressing the selected member clears it. */
function AssigneeFacet({
  projectId,
  workspaceId,
  selected,
}: {
  projectId: string;
  workspaceId: string;
  selected: string;
}): ReactElement {
  const { t } = useTranslation('common');
  const { data: users } = useWorkspaceUsersQuery(workspaceId);
  return (
    <FilterSection label={t('tasks.filters.assignee')}>
      {users.map((u) => (
        <ToggleChip
          key={u.id}
          pressed={u.id === selected}
          onPressedChange={(pressed) => {
            setTaskFilterAssignee(projectId, pressed ? u.id : '');
          }}
        >
          {u.displayName}
        </ToggleChip>
      ))}
    </FilterSection>
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
  actions,
}: TaskFiltersBarProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const listFormat = new Intl.ListFormat(i18n.resolvedLanguage ?? 'en', { type: 'conjunction' });
  const filters = useTaskFilters(projectId);
  const states = filters.states ?? [];
  const priority = filters.priority ?? [];
  const assigneeId = filters.assigneeId ?? '';

  const activeStates = states.length > 0;
  const activePriority = priority.length > 0;
  const activeAssignee = assigneeId.length > 0;
  const activeCount = [activeStates, activePriority, activeAssignee].filter(Boolean).length;
  const anyActive = activeCount > 0 || (filters.search ?? '').length > 0;

  const searchLabel = t('tasks.filters.search_placeholder');
  const clearLabel = t('tasks.filters.clear');
  const removeLabel = (label: string): string => t('tasks.filters.chip_remove', { label });

  const statusText = t('tasks.filters.chip_status', {
    value: listFormat.format(
      TASK_STATES.filter((s) => states.includes(s)).map((s) => t(STATE_KEY[s])),
    ),
  });
  const priorityText = t('tasks.filters.chip_priority', {
    value: listFormat.format(
      TASK_PRIORITIES.filter((p) => priority.includes(p)).map((p) => t(PRIORITY_KEY[p])),
    ),
  });

  const panel = (
    <div className={styles.panel}>
      <FilterSection label={t('tasks.filters.status')}>
        {TASK_STATES.map((state: TaskDerivedState) => (
          <ToggleChip
            key={state}
            pressed={states.includes(state)}
            onPressedChange={() => {
              setTaskFilterStates(projectId, toggled(TASK_STATES, states, state));
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
            pressed={priority.includes(p)}
            onPressedChange={() => {
              setTaskFilterPriority(projectId, toggled(TASK_PRIORITIES, priority, p));
            }}
          >
            {t(PRIORITY_KEY[p])}
          </ToggleChip>
        ))}
      </FilterSection>
      {workspaceId !== undefined ? (
        <Suspense fallback={<Skeleton className={styles.facetSkeleton} />}>
          <AssigneeFacet projectId={projectId} workspaceId={workspaceId} selected={assigneeId} />
        </Suspense>
      ) : null}
      <div className={styles.panelFooter}>
        <Button
          type="button"
          variant="ghost"
          disabled={!anyActive}
          onClick={() => {
            resetTaskFilters(projectId);
          }}
        >
          {clearLabel}
        </Button>
      </div>
    </div>
  );

  return (
    <div className={styles.bar}>
      <div className={styles.toolbar}>
        <div role="search" aria-label={t('tasks.title')} className={styles.searchInput}>
          <Tooltip content={t('tasks.filters.search_hint')}>
            <Input
              type="search"
              value={filters.search ?? ''}
              onChange={(e) => {
                setTaskFilterSearch(projectId, e.target.value);
              }}
              placeholder={searchLabel}
              aria-label={searchLabel}
            />
          </Tooltip>
        </div>
        <Popover
          placement="bottom-start"
          ariaLabel={t('tasks.filters.button')}
          className={styles.popoverPanel}
          content={panel}
        >
          <Button
            type="button"
            className={styles.filtersButton}
            data-active={activeCount > 0 ? '' : undefined}
            aria-label={t('tasks.filters.button_label', { count: activeCount })}
          >
            <ListFilter aria-hidden size="1em" />
            <span className={styles.collapsibleLabel}>{t('tasks.filters.button')}</span>
            {activeCount > 0 ? (
              <Badge tone="accent" aria-hidden className={styles.count}>
                {activeCount}
              </Badge>
            ) : null}
          </Button>
        </Popover>
        {workspaceId !== undefined ? (
          <Suspense fallback={<Skeleton className={styles.lensSkeleton} />}>
            <LensPicker workspaceId={workspaceId} projectId={projectId} />
          </Suspense>
        ) : null}
        {actions ? <div className={styles.actions}>{actions}</div> : null}
      </div>

      <Collapse open={activeCount > 0}>
        <div role="status" aria-live="polite" className={styles.chipsRow}>
          {activeStates ? (
            <Chip
              onDismiss={() => {
                setTaskFilterStates(projectId, []);
              }}
              dismissLabel={removeLabel(statusText)}
            >
              {statusText}
            </Chip>
          ) : null}
          {activePriority ? (
            <Chip
              onDismiss={() => {
                setTaskFilterPriority(projectId, []);
              }}
              dismissLabel={removeLabel(priorityText)}
            >
              {priorityText}
            </Chip>
          ) : null}
          {activeAssignee && workspaceId !== undefined ? (
            <Suspense fallback={null}>
              <AssigneeChip
                workspaceId={workspaceId}
                assigneeId={assigneeId}
                onDismiss={() => {
                  setTaskFilterAssignee(projectId, '');
                }}
              />
            </Suspense>
          ) : null}
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              resetTaskFilters(projectId);
            }}
            className={styles.clearButton}
          >
            {clearLabel}
          </Button>
        </div>
      </Collapse>
    </div>
  );
}
