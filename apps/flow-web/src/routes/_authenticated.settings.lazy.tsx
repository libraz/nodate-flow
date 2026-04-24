/**
 * /settings — pathless layout for the settings area (lazy). Renders a
 * sub-nav and an outlet for the active settings page. On viewports
 * < 768px the sub-nav collapses into a horizontally scrollable tab strip
 * stacked above the outlet (see `_authenticated.settings.module.css`).
 */

import { Link, Outlet, createLazyFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './_authenticated.settings.module.css';

interface SubNavItem {
  key: 'profile' | 'notifications' | 'security' | 'integrations';
  to:
    | '/settings/profile'
    | '/settings/notifications'
    | '/settings/security'
    | '/settings/integrations';
}

const SUB_NAV: readonly SubNavItem[] = [
  { key: 'profile', to: '/settings/profile' },
  { key: 'notifications', to: '/settings/notifications' },
  { key: 'security', to: '/settings/security' },
  { key: 'integrations', to: '/settings/integrations' },
];

function SettingsLayout(): ReactElement {
  const { t } = useTranslation('settings');

  const labelKeyFor = (
    key: SubNavItem['key'],
  ): 'nav.profile' | 'nav.notifications' | 'nav.security' | 'nav.integrations' => {
    switch (key) {
      case 'profile':
        return 'nav.profile';
      case 'notifications':
        return 'nav.notifications';
      case 'security':
        return 'nav.security';
      case 'integrations':
        return 'nav.integrations';
    }
  };

  return (
    <section className={styles.layout}>
      <nav aria-label={t('settings_sections_label')} className={styles.nav}>
        {SUB_NAV.map((item) => (
          <Link
            key={item.key}
            to={item.to}
            className={styles.link}
            activeProps={{ 'aria-current': 'page' }}
          >
            {t(labelKeyFor(item.key))}
          </Link>
        ))}
      </nav>
      <div>
        <Outlet />
      </div>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/settings')({
  component: SettingsLayout,
});
