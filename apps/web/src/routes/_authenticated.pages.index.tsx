/**
 * /pages — index route that renders the PageList component.
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import PageList from '../features/pages/page-list';

function PagesIndex(): ReactElement {
  return <PageList activePageId={undefined} />;
}

export const Route = createFileRoute('/_authenticated/pages/')({
  component: PagesIndex,
});
