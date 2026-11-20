/**
 * /pages/$pageId — individual page view route. The page's own title is
 * the h1, so the frame carries no PageHeader. `?mode=edit` opens the
 * page straight in the editor (used after AI generation creates a page).
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import PageFrame from '../components/page-frame';
import PageList from '../features/pages/page-list';
import styles from '../features/pages/pages.module.css';

function PageView(): ReactElement {
  const { pageId } = Route.useParams();
  const { mode } = Route.useSearch();
  return (
    <PageFrame width="full" className={styles.frame}>
      <PageList activePageId={pageId} initialMode={mode} />
    </PageFrame>
  );
}

export const Route = createFileRoute('/_authenticated/pages/$pageId')({
  component: PageView,
  validateSearch: (raw: Record<string, unknown>): { mode?: 'edit' } =>
    raw.mode === 'edit' ? { mode: 'edit' } : {},
});
