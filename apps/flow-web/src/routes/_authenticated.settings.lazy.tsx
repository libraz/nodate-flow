/**
 * /settings — pathless layout for the settings area (lazy). Renders a left
 * sub-nav and an outlet for the active settings page.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import { Link, Outlet, createLazyFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

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
    <section
      style={{
        display: 'grid',
        gridTemplateColumns: '16rem 1fr',
        gap: '2rem',
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
        maxInlineSize: '72rem',
        marginInline: 'auto',
        inlineSize: '100%',
      }}
    >
      <nav
        aria-label={t('settings_sections_label')}
        style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}
      >
        {SUB_NAV.map((item) => (
          <Link
            key={item.key}
            to={item.to}
            className={cx('settings-subnav-link')}
            activeProps={{
              'aria-current': 'page',
              style: {
                background: 'var(--nf-color-accent-subtle, rgba(155,89,182,0.12))',
                color: 'var(--nf-color-accent, var(--nf-color-accent))',
                fontWeight: 500,
              },
            }}
            style={{
              display: 'block',
              padding: '0.5rem 0.75rem',
              borderRadius: '0.5rem',
              color: 'var(--nf-color-fg)',
              textDecoration: 'none',
            }}
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
