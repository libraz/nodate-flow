/**
 * TaskParentLink — breadcrumb line above the detail title naming the parent
 * of a subtask. The parent is loaded by id; if it cannot be read (no access,
 * deleted) the line is simply omitted rather than breaking the page.
 */

import { Link } from '@tanstack/react-router';
import { CornerDownRight } from 'lucide-react';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';
import { useTaskQuery } from './api';
import css from './task-parent-link.module.css';

export interface TaskParentLinkProps {
  parentId: string;
}

function ParentLine({ parentId }: TaskParentLinkProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: parent } = useTaskQuery(parentId);
  return (
    <p className={css.line}>
      <CornerDownRight size={12} aria-hidden className={css.icon} />
      <span className={css.label}>{t('tasks.detail.parent_label')}</span>
      <Link
        to="/tasks/$taskId"
        params={{ taskId: parentId }}
        className={css.link}
        title={parent.title}
      >
        {parent.title}
      </Link>
    </p>
  );
}

export default function TaskParentLink({ parentId }: TaskParentLinkProps): ReactElement {
  return (
    <ErrorBoundary fallback={null}>
      <Suspense fallback={null}>
        <ParentLine parentId={parentId} />
      </Suspense>
    </ErrorBoundary>
  );
}
