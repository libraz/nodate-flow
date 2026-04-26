/**
 * CalendarSettingsDrawer — host for per-calendar admin operations.
 *
 * Mounts as a right-side drawer triggered from the calendars rail's
 * row menu. The drawer is split into three tabs:
 *
 *   - General (W13): rename, change color, edit description, delete.
 *   - Members (W12): future home of the Calendar Members manager.
 *   - Sync   (placeholder): reserved for future sync settings.
 *
 * The drawer manages tab state internally and lazily mounts each
 * tab's body so a slow query in one panel doesn't block the others.
 * Loading the calendar resource happens in a Suspense boundary scoped
 * to the General tab — Members will adopt the same pattern.
 */

import Drawer from '@nodate-flow/ui/primitives/drawer';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Tabs from '@nodate-flow/ui/primitives/tabs';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import CalendarMembersTab from './calendar-members-tab';
import styles from './calendar-settings-drawer.module.css';
import GeneralTab from './general-tab';

export interface CalendarSettingsDrawerProps {
  workspaceId: string;
  calendarId: string;
  open: boolean;
  onClose: () => void;
}

export default function CalendarSettingsDrawer({
  workspaceId,
  calendarId,
  open,
  onClose,
}: CalendarSettingsDrawerProps): ReactElement {
  const { t } = useTranslation('common');

  const fallback = (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
      <Skeleton style={{ blockSize: '2rem', inlineSize: '60%' }} />
      <Skeleton style={{ blockSize: '2.5rem', inlineSize: '100%' }} />
      <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
    </div>
  );

  return (
    <Drawer open={open} onClose={onClose} title={t('calendar.settings.title')} side="inline-end">
      <div className={styles.body}>
        <Tabs
          aria-label={t('calendar.settings.title')}
          defaultValue="general"
          className={styles.tabs}
          items={[
            {
              value: 'general',
              label: t('calendar.settings.tab.general'),
              content: (
                <Suspense fallback={fallback}>
                  <GeneralTab
                    workspaceId={workspaceId}
                    calendarId={calendarId}
                    onAfterDelete={onClose}
                  />
                </Suspense>
              ),
            },
            {
              value: 'members',
              label: t('calendar.settings.tab.members'),
              content: (
                <Suspense fallback={fallback}>
                  <CalendarMembersTab workspaceId={workspaceId} calendarId={calendarId} />
                </Suspense>
              ),
            },
            {
              value: 'sync',
              label: t('calendar.settings.tab.sync'),
              content: (
                <p className={styles.placeholder}>{t('calendar.settings.sync.placeholder')}</p>
              ),
            },
          ]}
        />
      </div>
    </Drawer>
  );
}
