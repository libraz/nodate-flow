/**
 * /settings/notifications — edit the authenticated user's notification
 * channel toggles (lazy).
 */

import { createLazyFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import NotificationsForm, {
  NotificationsFormSkeleton,
} from '../features/settings/notifications-form';
import ui from '../features/settings/settings-ui.module.css';

function NotificationsRoute(): ReactElement {
  const { t } = useTranslation('settings');
  return (
    <div className={ui.page}>
      <header className={ui.header}>
        <h1 className={ui.title}>{t('notifications.title')}</h1>
      </header>
      <Suspense fallback={<NotificationsFormSkeleton />}>
        <NotificationsForm />
      </Suspense>
    </div>
  );
}

export const Route = createLazyFileRoute('/_authenticated/settings/notifications')({
  component: NotificationsRoute,
});
