import { Link, useRouterState } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from '../workspaces/workspaces.module.css';

/** Timeline / activity switcher shown under both history pages. */
export default function HistoryTabs({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('common');
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const onActivity = pathname.startsWith(`/workspaces/${workspaceId}/activity`);
  return (
    <nav aria-label={t('nav.history')} className={styles.subNav}>
      <Link
        to="/workspaces/$id/timeline"
        params={{ id: workspaceId }}
        title={t('nav.hint.timeline')}
        aria-current={onActivity ? undefined : 'page'}
        className={styles.subNavLink}
      >
        {t('nav.timeline')}
      </Link>
      <Link
        to="/workspaces/$id/activity"
        params={{ id: workspaceId }}
        title={t('nav.hint.activity')}
        aria-current={onActivity ? 'page' : undefined}
        className={styles.subNavLink}
      >
        {t('nav.activity')}
      </Link>
    </nav>
  );
}
