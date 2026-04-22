/**
 * Branded "not found" component used by both the root route's
 * `notFoundComponent` and the authenticated layout's
 * `notFoundComponent`. Renders a large gradient 404 mark, a short
 * description, and a back-home button.
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
        gap: '1.25rem',
        padding: '3rem 2rem',
        textAlign: 'center',
      }}
    >
      <div
        aria-hidden
        style={{
          fontFamily: 'var(--font-display)',
          fontSize: 'clamp(5rem, 14vw, 9rem)',
          lineHeight: 1,
          fontWeight: 700,
          backgroundImage: 'var(--nf-gradient-wordmark)',
          backgroundClip: 'text',
          // biome-ignore lint/style/useNamingConvention: vendor prefix
          WebkitBackgroundClip: 'text',
          color: 'transparent',
          // biome-ignore lint/style/useNamingConvention: vendor prefix
          WebkitTextFillColor: 'transparent',
        }}
      >
        {t('not_found.code', { defaultValue: '404' })}
      </div>
      <h1
        style={{
          fontFamily: 'var(--font-display)',
          margin: 0,
          fontSize: '1.5rem',
          color: 'var(--nf-color-fg)',
        }}
      >
        {t('not_found.title')}
      </h1>
      <p
        style={{
          margin: 0,
          maxInlineSize: '28rem',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {t('not_found.description')}
      </p>
      <Link
        to="/"
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          padding: '0.5rem 1.25rem',
          borderRadius: '0.5rem',
          background: 'var(--nf-color-accent, var(--nf-color-accent))',
          color: 'var(--nf-color-fg-on-accent, white)',
          textDecoration: 'none',
          fontWeight: 500,
        }}
      >
        {t('not_found.back_home')}
      </Link>
    </section>
  );
}
