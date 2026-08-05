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
          paddingBlockStart: 'clamp(var(--nf-space-6), 4vw, var(--nf-space-10))',
          paddingInline: 'clamp(var(--nf-space-6), 4vw, var(--nf-space-10))',
          fontFamily: 'var(--nf-font-display)',
          fontSize: 'clamp(1.75rem, 3vw, var(--nf-text-4xl))',
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
