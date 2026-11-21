/**
 * /settings — pathless layout for the settings area (lazy). Renders a
 * sub-nav and an outlet for the active settings page. On viewports
 * < 768px the sub-nav collapses into a horizontally scrollable tab strip
 * stacked above the outlet (see `_authenticated.settings.module.css`).
 * The outlet is keyed on the path so each sub-page fades in on arrival.
 */

import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import { createLazyFileRoute, Link, Outlet, useRouterState } from '@tanstack/react-router';
import { ExternalLink } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import PageFrame from '../components/page-frame';
import { accountsWebUrl } from '../lib/accounts-web-url';
import styles from './_authenticated.settings.module.css';

interface SubNavItem {
  key: 'notifications' | 'integrations';
  to: '/settings/notifications' | '/settings/integrations';
}

const SUB_NAV: readonly SubNavItem[] = [
  { key: 'notifications', to: '/settings/notifications' },
  { key: 'integrations', to: '/settings/integrations' },
];

function SettingsLayout(): ReactElement {
  const { t } = useTranslation('settings');
  const pathname = useRouterState({ select: (state) => state.location.pathname });

  const labelKeyFor = (key: SubNavItem['key']): 'nav.notifications' | 'nav.integrations' => {
    switch (key) {
      case 'notifications':
        return 'nav.notifications';
      case 'integrations':
        return 'nav.integrations';
    }
  };

  return (
    <PageFrame>
      <section className={styles.layout}>
        <div className={styles.navColumn}>
          <nav
            aria-label={t('settings_sections_label')}
            className={cx(styles.nav, styles.navStatic)}
          >
            <a href={accountsWebUrl('/profile')} className={styles.link}>
              <span className={styles.externalLabel}>
                {t('nav.account')}
                <Icon icon={ExternalLink} size={12} decorative />
              </span>
            </a>
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
          <p className={styles.accountHint}>{t('nav.account_hint')}</p>
        </div>
        <div key={pathname} className={styles.outlet}>
          <Outlet />
        </div>
      </section>
    </PageFrame>
  );
}

export const Route = createLazyFileRoute('/_authenticated/settings')({
  component: SettingsLayout,
});
