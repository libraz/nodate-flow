/**
 * Leading control of a nested task row (indent plus the expand/collapse
 * chevron) and the loaders that fetch the children of expanded rows.
 */

import Spinner from '@nodate-flow/ui/primitives/spinner';
import { ChevronRight } from 'lucide-react';
import { type CSSProperties, type ReactElement, Suspense } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';
import { useProjectQuery } from '../projects/api';
import { useSubtasksQuery } from './api';
import css from './subtask-disclosure.module.css';
import type { TreeRow } from './use-subtask-tree';

export interface SubtaskLeadProps {
  row: TreeRow;
  onToggle: (taskId: string) => void;
}

/** Indent for the row's depth, then the chevron, or an equal gap when there is nothing to expand. */
export function SubtaskLead({ row, onToggle }: SubtaskLeadProps): ReactElement {
  const { t } = useTranslation('common');
  const { task, expanded, loading } = row;
  const label = expanded
    ? t('tasks.hierarchy.hide_subtasks', { title: task.title })
    : t('tasks.hierarchy.show_subtasks', { title: task.title });
  return (
    <span className={css.lead} style={{ '--subtask-depth': row.depth } as CSSProperties}>
      {row.expandable ? (
        <button
          type="button"
          className={css.toggle}
          aria-expanded={expanded}
          aria-busy={loading || undefined}
          aria-label={label}
          title={label}
          onClick={(e) => {
            // The row's own click (inline edit) must not fire.
            e.stopPropagation();
            onToggle(task.id);
          }}
          onKeyDown={(e) => {
            // Enter and Space activate the button; the cell must not start an edit.
            if (e.key === 'Enter' || e.key === ' ') e.stopPropagation();
          }}
        >
          {loading ? (
            <Spinner size="sm" label={label} aria-hidden="true" />
          ) : (
            <ChevronRight size={14} aria-hidden className={css.chevron} />
          )}
        </button>
      ) : (
        <span className={css.gap} aria-hidden="true" />
      )}
    </span>
  );
}

/** Keeps `useSubtasksQuery` mounted, and so the cache fresh, for one expanded parent. */
function SubtaskLoader({ projectId, parentId }: { projectId: string; parentId: string }): null {
  const { data: project } = useProjectQuery(projectId);
  useSubtasksQuery(parentId, project.workspaceId);
  return null;
}

/**
 * One boundary per expanded parent. Rows show their loading state themselves,
 * and a failed load leaves that parent without children rather than taking
 * the list down.
 */
export function SubtaskLoaders({
  projectId,
  parentIds,
}: {
  projectId: string;
  parentIds: readonly string[];
}): ReactElement {
  return (
    <>
      {parentIds.map((parentId) => (
        <ErrorBoundary key={parentId} fallback={null}>
          <Suspense fallback={null}>
            <SubtaskLoader projectId={projectId} parentId={parentId} />
          </Suspense>
        </ErrorBoundary>
      ))}
    </>
  );
}
