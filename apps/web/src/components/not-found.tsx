/**
 * Branded "not found" component used by both the root route's
 * `notFoundComponent` and the authenticated layout's
 * `notFoundComponent`. Renders a minimal centered call-to-action that
 * links back to the app home.
 *
 * The authenticated layout wraps this in `<AppShell>` so deep-link 404s
 * land inside the normal sidebar/topbar chrome instead of replacing the
 * entire viewport with a bare error message.
 */

import { Link } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

export default function NotFound(): ReactElement {
  const { t } = useTranslation('common');
  return (
    <section
      style={{
        minBlockSize: '60vh',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: '1rem',
        padding: '2rem',
        fontFamily: 'var(--font-body)',
        color: 'var(--color-fg)',
      }}
    >
      <h1 style={{ fontFamily: 'var(--font-display)', margin: 0 }}>{t('not_found.title')}</h1>
      <p style={{ margin: 0, color: 'var(--color-muted)' }}>{t('not_found.description')}</p>
      <Link to="/" style={{ color: 'var(--color-fg)' }}>
        {t('not_found.back_home')}
      </Link>
    </section>
  );
}
