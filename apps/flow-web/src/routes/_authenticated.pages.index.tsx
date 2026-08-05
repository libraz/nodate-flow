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
          paddingBlockStart: 'clamp(1.5rem, 4vw, 2.5rem)',
          paddingInline: 'clamp(1.5rem, 4vw, 2.5rem)',
          fontFamily: 'var(--nf-font-display)',
          fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
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
