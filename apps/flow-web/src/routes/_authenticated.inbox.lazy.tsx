/**
 * /inbox — caller's inbox, signal-backed. Wraps `<InboxList />` in Suspense
 * with a simple themed layout matching the rest of the authenticated shell.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import InboxList from '../features/inbox/inbox-list';

function InboxRoute(): ReactElement {
  const { t } = useTranslation('inbox');
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
      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
            <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <InboxList />
      </Suspense>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/inbox')({
  component: InboxRoute,
});
