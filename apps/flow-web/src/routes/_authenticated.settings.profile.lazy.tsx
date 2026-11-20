/**
 * /settings/profile — edit the authenticated user's profile (lazy).
 */

import { createLazyFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import PageHeader from '../components/page-header';
import ProfileForm, { ProfileFormSkeleton } from '../features/settings/profile-form';
import ui from '../features/settings/settings-ui.module.css';

function ProfileRoute(): ReactElement {
  const { t } = useTranslation('settings');
  return (
    <div className={ui.page}>
      <PageHeader
        title={t('profile.title')}
        description={t('profile.description')}
        help="settings-profile"
      />
      <Suspense fallback={<ProfileFormSkeleton />}>
        <ProfileForm />
      </Suspense>
    </div>
  );
}

export const Route = createLazyFileRoute('/_authenticated/settings/profile')({
  component: ProfileRoute,
});
