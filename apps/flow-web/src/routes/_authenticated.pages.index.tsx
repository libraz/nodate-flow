/**
 * /pages — index route that renders the PageList component.
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import PageFrame from '../components/page-frame';
import PageHeader from '../components/page-header';
import PageList from '../features/pages/page-list';
import styles from '../features/pages/pages.module.css';

function PagesIndex(): ReactElement {
  const { t } = useTranslation('pages');
  return (
    <PageFrame width="full" className={styles.frame}>
      <PageHeader title={t('title')} />
      <PageList activePageId={undefined} />
    </PageFrame>
  );
}

export const Route = createFileRoute('/_authenticated/pages/')({
  component: PagesIndex,
});
