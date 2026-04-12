/**
 * /pages/$pageId — individual page view route.
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import PageList from '../features/pages/page-list';

function PageView(): ReactElement {
  const { pageId } = Route.useParams();
  return <PageList activePageId={pageId} />;
}

export const Route = createFileRoute('/_authenticated/pages/$pageId')({
  component: PageView,
});
