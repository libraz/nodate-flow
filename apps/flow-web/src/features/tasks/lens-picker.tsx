/**
 * LensPicker — saved-views (lens) listing + management.
 *
 * Hosted inside the design-system `Popover` primitive: it owns the
 * positioning, focus trap, dismiss-on-Escape / outside click, and aria
 * roles. The picker is still feature-local because the savedviews CRUD
 * shape is tasks-specific.
 *
 * `aria-selected` is a real comparison between the current task filters and each lens's stored
 * filter map.
 *
 * Deleting a lens is offered only to the people the API lets do it — its
 * creator and workspace admins / owners. See {@link canManageLens}.
 */

import Popover from '@nodate-flow/ui/primitives/popover';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { selectUser, useAuth } from '../auth/auth-store';
import { useWorkspaceQuery } from '../workspaces/api';
import type { TaskDerivedState, TaskFilters, TaskPriority } from './api';
import type { LensDto } from './lens-api';
import { useCreateLens, useDeleteLens, useLensesQuery } from './lens-api';
import styles from './lens-picker.module.css';
import { getTaskFilters, setTaskFilters, useTaskFilters } from './use-task-filters';

export interface LensPickerProps {
  workspaceId: string;
  projectId: string;
}

/**
 * Best-effort mapping from the generic lens filter map to the in-memory
 * TaskFilters shape the board/list already consumes.
 */
function lensFilterToTaskFilters(filter: Record<string, Record<string, unknown>>): TaskFilters {
  const out: TaskFilters = {};

  if (filter.priority) {
    const vals = filter.priority.values;
    if (Array.isArray(vals)) {
      out.priority = vals.filter(
        (v): v is TaskPriority => typeof v === 'number' && v >= 0 && v <= 4,
      );
    }
  }

  if (filter.status) {
    const vals = filter.status.values;
    if (Array.isArray(vals)) {
      out.states = vals.filter(
        (v): v is TaskDerivedState =>
          typeof v === 'string' && ['open', 'waiting', 'review', 'done', 'cancelled'].includes(v),
      );
    }
  }

  if (filter.assignee) {
    const val = filter.assignee.value;
    if (typeof val === 'string') out.assigneeId = val;
  }

  if (filter.search) {
    const val = filter.search.value;
    if (typeof val === 'string') out.search = val;
  }

  return out;
}

/** Convert current TaskFilters back to the lens filter map format. */
function taskFiltersToLensFilter(filters: TaskFilters): Record<string, Record<string, unknown>> {
  const out: Record<string, Record<string, unknown>> = {};
  if (filters.states && filters.states.length > 0) {
    out.status = { values: [...filters.states] };
  }
  if (filters.priority && filters.priority.length > 0) {
    out.priority = { values: [...filters.priority] };
  }
  if (filters.assigneeId && filters.assigneeId.length > 0) {
    out.assignee = { value: filters.assigneeId };
  }
  if (filters.search && filters.search.length > 0) {
    out.search = { value: filters.search };
  }
  return out;
}

/**
 * Compare a lens's saved filter against the currently active filter so
 * the dropdown can mark the matching row with `aria-selected`. Order
 * within state/priority value arrays is treated as significant only as
 * a multi-set: we compare sorted projections to avoid false negatives
 * after a re-application of the same lens.
 */
function isLensActive(
  lensFilter: Record<string, Record<string, unknown>>,
  current: TaskFilters,
): boolean {
  const lensShape = lensFilterToTaskFilters(lensFilter);

  const sameStringArray = (a?: readonly string[], b?: readonly string[]): boolean => {
    const aa = (a ?? []).slice().sort();
    const bb = (b ?? []).slice().sort();
    if (aa.length !== bb.length) return false;
    for (let i = 0; i < aa.length; i++) if (aa[i] !== bb[i]) return false;
    return true;
  };
  const sameNumberArray = (a?: readonly number[], b?: readonly number[]): boolean => {
    const aa = (a ?? []).slice().sort();
    const bb = (b ?? []).slice().sort();
    if (aa.length !== bb.length) return false;
    for (let i = 0; i < aa.length; i++) if (aa[i] !== bb[i]) return false;
    return true;
  };

  return (
    sameStringArray(lensShape.states, current.states) &&
    sameNumberArray(lensShape.priority, current.priority) &&
    (lensShape.assigneeId ?? '') === (current.assigneeId ?? '') &&
    (lensShape.search ?? '') === (current.search ?? '')
  );
}

/**
 * Whether the signed-in user may write to this lens, mirroring the API rule:
 * the lens creator, or a workspace admin / owner. Anyone else gets
 * `WS.MEMBER.ROLE_DENIED` from update, delete, publish and unpublish, so the
 * affordance is not offered.
 *
 * A refusal can still arrive — the role can change, or another tab can act
 * first — and the mutation handlers surface it.
 *
 * `workspaceRole` is the caller's own role as the workspace endpoint reports
 * it, not a row looked up in the member list: that list is paginated, so an
 * admin sitting past the first page would be denied a control the API would
 * have allowed.
 */
function canManageLens(
  lens: LensDto,
  currentUserId: string | undefined,
  workspaceRole: string | undefined,
): boolean {
  if (workspaceRole === 'admin' || workspaceRole === 'owner') return true;
  return currentUserId !== undefined && lens.creatorId === currentUserId;
}

export default function LensPicker({ workspaceId, projectId }: LensPickerProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: lenses } = useLensesQuery(workspaceId, projectId);
  const { data: workspace } = useWorkspaceQuery(workspaceId);
  const currentUser = useAuth(selectUser);
  const currentFilters = useTaskFilters(projectId);
  const createLens = useCreateLens();
  const deleteLens = useDeleteLens();

  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [nameInput, setNameInput] = useState('');
  const [descriptionInput, setDescriptionInput] = useState('');

  const handleApply = (lens: LensDto): void => {
    const filters = lensFilterToTaskFilters(lens.filter);
    setTaskFilters(projectId, filters);
    setOpen(false);
  };

  const handleDelete = async (lens: LensDto): Promise<void> => {
    try {
      await deleteLens.mutateAsync({ workspaceId, lensId: lens.id, projectId });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.lens.delete_failed'),
      });
    }
  };

  const handleSave = async (): Promise<void> => {
    const trimmed = nameInput.trim();
    if (trimmed.length === 0) return;
    const trimmedDescription = descriptionInput.trim();
    const captured = getTaskFilters(projectId);
    const filter = taskFiltersToLensFilter(captured);
    setSaving(true);
    try {
      await createLens.mutateAsync({
        workspaceId,
        name: trimmed,
        ...(trimmedDescription.length > 0 ? { description: trimmedDescription } : {}),
        projectId,
        filter,
      });
      setNameInput('');
      setDescriptionInput('');
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.lens.save_failed'),
      });
    } finally {
      setSaving(false);
    }
  };

  const panel = (
    <div className={styles.panel}>
      {lenses.length === 0 ? (
        <p className={styles.empty}>{t('tasks.lens.empty')}</p>
      ) : (
        <ul role="listbox" aria-label={t('tasks.lens.title')} className={styles.list}>
          {lenses.map((lens) => {
            const selected = isLensActive(lens.filter, currentFilters);
            return (
              <li
                key={lens.id}
                role="option"
                aria-selected={selected}
                className={`${styles.option} ${selected ? styles.optionSelected : ''}`.trim()}
              >
                <button
                  type="button"
                  className={styles.optionApply}
                  onClick={() => {
                    handleApply(lens);
                  }}
                >
                  {lens.name}
                  {lens.isDefault ? (
                    <span className={styles.defaultBadge}>{t('tasks.lens.default_badge')}</span>
                  ) : null}
                </button>
                {canManageLens(lens, currentUser?.id, workspace.role) ? (
                  <button
                    type="button"
                    aria-label={t('tasks.lens.delete')}
                    className={styles.optionDelete}
                    onClick={() => {
                      void handleDelete(lens);
                    }}
                  >
                    &times;
                  </button>
                ) : null}
              </li>
            );
          })}
        </ul>
      )}

      <div className={styles.saveRow}>
        <input
          type="text"
          value={nameInput}
          onChange={(e) => {
            setNameInput(e.target.value);
          }}
          placeholder={t('tasks.lens.name_placeholder')}
          aria-label={t('tasks.lens.name_placeholder')}
          className={styles.saveInput}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void handleSave();
          }}
        />
        <button
          type="button"
          disabled={saving || nameInput.trim().length === 0}
          onClick={() => {
            void handleSave();
          }}
          className={styles.saveButton}
        >
          {saving ? t('tasks.lens.creating') : t('tasks.lens.create')}
        </button>
      </div>

      <textarea
        value={descriptionInput}
        onChange={(e) => {
          setDescriptionInput(e.target.value);
        }}
        placeholder={t('tasks.lens.description_placeholder')}
        aria-label={t('tasks.lens.description_placeholder')}
        className={styles.descriptionInput}
        maxLength={500}
        rows={2}
      />
    </div>
  );

  return (
    <Popover open={open} onOpenChange={setOpen} placement="bottom-end" content={panel}>
      <button type="button" aria-haspopup="listbox" className={styles.trigger}>
        {t('tasks.lens.title')}
      </button>
    </Popover>
  );
}
