/**
 * /settings/notifications — edit the authenticated user's notification
 * channel toggles (lazy).
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import NotificationsForm from '../features/settings/notifications-form';

function NotificationsRoute(): ReactElement {
  const { t } = useTranslation('settings');
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem' }}>
      <h1 style={{ margin: 0, fontSize: 'var(--nf-text-2xl)' }}>{t('notifications.title')}</h1>
      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
            <Skeleton style={{ blockSize: '3rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '3rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '3rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <NotificationsForm />
      </Suspense>
    </div>
  );
}

export const Route = createLazyFileRoute('/_authenticated/settings/notifications')({
  component: NotificationsRoute,
});
