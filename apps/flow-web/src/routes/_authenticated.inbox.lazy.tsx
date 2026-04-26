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

function InboxRoute(): ReactElement {
  const { t } = useTranslation('inbox');

  const fallback = (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
      <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
      <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
      <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
    </div>
  );

  return (
    <section
      style={{
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        maxInlineSize: '60rem',
        marginInline: 'auto',
        inlineSize: '100%',
      }}
    >
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h1
          style={{
            margin: 0,
            fontFamily: 'var(--font-display)',
            fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
          }}
        >
          {t('view.title')}
        </h1>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>{t('view.subtitle')}</p>
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
