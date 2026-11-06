/**
 * /pages/$pageId — individual page view route. The page's own title is
 * the h1, so the frame carries no PageHeader.
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import PageFrame from '../components/page-frame';
import PageList from '../features/pages/page-list';
import styles from '../features/pages/pages.module.css';

function PageView(): ReactElement {
  const { pageId } = Route.useParams();
  return (
    <PageFrame width="full" className={styles.frame}>
      <PageList activePageId={pageId} />
    </PageFrame>
  );
}

export const Route = createFileRoute('/_authenticated/pages/$pageId')({
  component: PageView,
});
