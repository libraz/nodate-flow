/**
 * LensPicker — lightweight dropdown for listing, applying, creating, and
 * deleting saved views (lenses). Wrapped in Suspense at the mount site.
 */

import { type ReactElement, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { TaskDerivedState, TaskFilters, TaskPriority } from './api';
import type { LensDto } from './lens-api';
import { useCreateLens, useDeleteLens, useLensesQuery } from './lens-api';
import { getTaskFilters, setTaskFilters } from './use-task-filters';

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

export default function LensPicker({ workspaceId, projectId }: LensPickerProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: lenses } = useLensesQuery(workspaceId, projectId);
  const createLens = useCreateLens();
  const deleteLens = useDeleteLens();

  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [nameInput, setNameInput] = useState('');
  const triggerRef = useRef<HTMLButtonElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);

  const handleApply = (lens: LensDto): void => {
    const filters = lensFilterToTaskFilters(lens.filter);
    setTaskFilters(projectId, filters);
    setOpen(false);
  };

  const handleDelete = (lens: LensDto): void => {
    deleteLens.mutate({ workspaceId, lensId: lens.id, projectId });
  };

  const handleSave = (): void => {
    const trimmed = nameInput.trim();
    if (trimmed.length === 0) return;
    const currentFilters = getTaskFilters(projectId);
    const filter = taskFiltersToLensFilter(currentFilters);
    setSaving(true);
    createLens.mutate(
      { workspaceId, name: trimmed, projectId, filter },
      {
        onSettled: () => {
          setSaving(false);
          setNameInput('');
        },
      },
    );
  };

  return (
    <div style={{ position: 'relative', display: 'inline-block' }}>
      <button
        ref={triggerRef}
        type="button"
        aria-expanded={open}
        aria-haspopup="listbox"
        onClick={() => {
          setOpen((prev) => !prev);
        }}
        style={{
          background: 'none',
          border: '1px solid var(--nf-color-border)',
          borderRadius: '0.375rem',
          padding: '0.25rem 0.625rem',
          color: 'var(--nf-color-fg-muted)',
          cursor: 'pointer',
          font: 'inherit',
          fontSize: '0.8125rem',
        }}
      >
        {t('tasks.lens.title')}
      </button>

      {open ? (
        <div
          ref={dropdownRef}
          role="listbox"
          aria-label={t('tasks.lens.title')}
          style={{
            position: 'absolute',
            insetBlockStart: '100%',
            insetInlineEnd: '0',
            marginBlockStart: '0.25rem',
            background: 'var(--nf-color-bg)',
            border: '1px solid var(--nf-color-border)',
            borderRadius: '0.5rem',
            boxShadow: '0 4px 12px oklch(0 0 0 / 12%)',
            minInlineSize: '14rem',
            maxInlineSize: '20rem',
            zIndex: 50,
            padding: '0.375rem',
          }}
        >
          {lenses.length === 0 ? (
            <p
              style={{
                padding: '0.5rem',
                margin: 0,
                color: 'var(--nf-color-fg-muted)',
                fontSize: '0.8125rem',
              }}
            >
              {t('tasks.lens.empty')}
            </p>
          ) : (
            <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
              {lenses.map((lens) => (
                <li
                  key={lens.id}
                  role="option"
                  aria-selected={false}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '0.375rem',
                    padding: '0.375rem 0.5rem',
                    borderRadius: '0.25rem',
                    cursor: 'pointer',
                    fontSize: '0.8125rem',
                  }}
                >
                  <button
                    type="button"
                    onClick={() => {
                      handleApply(lens);
                    }}
                    style={{
                      flex: 1,
                      background: 'none',
                      border: 'none',
                      padding: 0,
                      textAlign: 'start',
                      cursor: 'pointer',
                      font: 'inherit',
                      color: 'var(--nf-color-fg)',
                    }}
                  >
                    {lens.name}
                    {lens.isDefault ? (
                      <span
                        style={{
                          marginInlineStart: '0.375rem',
                          fontSize: '0.6875rem',
                          padding: '0.125rem 0.375rem',
                          borderRadius: '999px',
                          background: 'var(--nf-color-accent-subtle)',
                          color: 'var(--nf-color-accent)',
                        }}
                      >
                        {t('tasks.lens.default_badge')}
                      </span>
                    ) : null}
                  </button>
                  <button
                    type="button"
                    aria-label={t('tasks.lens.delete')}
                    onClick={() => {
                      handleDelete(lens);
                    }}
                    style={{
                      background: 'none',
                      border: 'none',
                      padding: '0.125rem 0.25rem',
                      cursor: 'pointer',
                      color: 'var(--nf-color-fg-muted)',
                      fontSize: 'var(--nf-text-xs)',
                      lineHeight: 1,
                    }}
                  >
                    &times;
                  </button>
                </li>
              ))}
            </ul>
          )}

          {/* Save current view form */}
          <div
            style={{
              borderBlockStart: '1px solid var(--nf-color-border)',
              marginBlockStart: '0.375rem',
              paddingBlockStart: '0.375rem',
              display: 'flex',
              gap: '0.25rem',
            }}
          >
            <input
              type="text"
              value={nameInput}
              onChange={(e) => {
                setNameInput(e.target.value);
              }}
              placeholder={t('tasks.lens.name_placeholder')}
              aria-label={t('tasks.lens.name_placeholder')}
              style={{
                flex: 1,
                border: '1px solid var(--nf-color-border)',
                borderRadius: '0.25rem',
                padding: '0.25rem 0.5rem',
                font: 'inherit',
                fontSize: '0.8125rem',
                background: 'var(--nf-color-bg)',
                color: 'var(--nf-color-fg)',
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleSave();
              }}
            />
            <button
              type="button"
              disabled={saving || nameInput.trim().length === 0}
              onClick={handleSave}
              style={{
                border: 'none',
                borderRadius: '0.25rem',
                padding: '0.25rem 0.5rem',
                font: 'inherit',
                fontSize: '0.8125rem',
                background: 'var(--nf-color-accent)',
                color: 'var(--nf-color-bg)',
                cursor: saving ? 'wait' : 'pointer',
                opacity: nameInput.trim().length === 0 ? 0.5 : 1,
              }}
            >
              {saving ? t('tasks.lens.creating') : t('tasks.lens.create')}
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
