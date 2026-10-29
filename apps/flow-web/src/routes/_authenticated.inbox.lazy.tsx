/**
 * /inbox — caller's inbox shell. Hosts a two-tab layout:
 *
 *   - Inbox: cross-workspace signal river surfaced by `<InboxList />`.
 *   - Intake: workspace-scoped triage queue surfaced by `<IntakeList />`.
 *
 * Tabs use the design system primitive so keyboard navigation, ARIA
 * roles, and the active-tab indicator stay consistent with the rest of
 * the authenticated shell. Each panel mounts its own Suspense boundary
 * so a slow workspace fetch in one tab doesn't blank the other.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Tabs from '@nodate-flow/ui/primitives/tabs';
import { createLazyFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import InboxList from '../features/inbox/inbox-list';
import IntakeList from '../features/inbox/intake/intake-list';
import styles from './_authenticated.inbox.module.css';

function InboxRoute(): ReactElement {
  const { t } = useTranslation('inbox');

  const fallback = (
    <div className={styles.fallback} aria-busy="true">
      <Skeleton className={styles.fallbackToolbar} />
      <Skeleton className={styles.fallbackRow} />
      <Skeleton className={styles.fallbackRow} />
      <Skeleton className={styles.fallbackRow} />
    </div>
  );

  return (
    <section className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('view.title')}</h1>
        <p className={styles.subtitle}>{t('view.subtitle')}</p>
      </header>
      <Tabs
        aria-label={t('view.title')}
        defaultValue="inbox"
        items={[
          {
            value: 'inbox',
            label: t('tab.inbox'),
            content: <Suspense fallback={fallback}>{<InboxList />}</Suspense>,
          },
          {
            value: 'intake',
            label: t('tab.intake'),
            content: <Suspense fallback={fallback}>{<IntakeList />}</Suspense>,
          },
        ]}
      />
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/inbox')({
  component: InboxRoute,
});
