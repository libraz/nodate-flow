/**
 * /settings/profile — edit the authenticated user's profile (lazy).
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import ProfileForm from '../features/settings/profile-form';

function ProfileRoute(): ReactElement {
  const { t } = useTranslation('settings');
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6)' }}>
      <h1 style={{ margin: 0, fontSize: 'var(--nf-text-2xl)' }}>{t('profile.title')}</h1>
      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3)' }}>
            <Skeleton style={{ blockSize: '2.5rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '2.5rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '2.5rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <ProfileForm />
      </Suspense>
    </div>
  );
}

export const Route = createLazyFileRoute('/_authenticated/settings/profile')({
  component: ProfileRoute,
});
