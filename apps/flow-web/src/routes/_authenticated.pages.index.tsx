/**
 * /pages — index route that renders the PageList component.
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import PageList from '../features/pages/page-list';

function PagesIndex(): ReactElement {
  const { t } = useTranslation('pages');
  return (
    <>
      <h1
        style={{
          margin: 0,
          paddingBlockStart: 'var(--nf-space-page)',
          paddingInline: 'var(--nf-space-page)',
          fontFamily: 'var(--nf-font-display)',
          fontSize: 'var(--nf-text-page-title)',
        }}
      >
        {t('title')}
      </h1>
      <PageList activePageId={undefined} />
    </>
  );
}

export const Route = createFileRoute('/_authenticated/pages/')({
  component: PagesIndex,
});
