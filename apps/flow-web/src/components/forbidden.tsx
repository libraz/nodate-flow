/**
 * Branded "forbidden" component rendered when the authenticated user
 * lacks permission to view a protected resource (HTTP 403). Mirrors
 * the NotFound component's visual treatment so deep-link 403s land on
 * a polished state instead of the generic ErrorBoundary.
 */

import { Link } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

export default function Forbidden(): ReactElement {
  const { t } = useTranslation('common');
  return (
    <section
      style={{
        minBlockSize: '60vh',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 'var(--nf-space-5)',
        padding: 'var(--nf-space-12) var(--nf-space-8)',
        textAlign: 'center',
      }}
    >
      <div
        aria-hidden
        style={{
          fontFamily: 'var(--nf-font-display)',
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
        {t('forbidden.code', { defaultValue: '403' })}
      </div>
      <h1
        style={{
          fontFamily: 'var(--nf-font-display)',
          margin: 0,
          fontSize: 'var(--nf-text-2xl)',
          color: 'var(--nf-color-fg)',
        }}
      >
        {t('forbidden.title')}
      </h1>
      <p
        style={{
          margin: 0,
          maxInlineSize: '28rem',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {t('forbidden.description')}
      </p>
      <Link
        to="/"
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          padding: 'var(--nf-space-2) var(--nf-space-5)',
          borderRadius: 'var(--nf-radius-md)',
          background: 'var(--nf-color-accent)',
          color: 'var(--nf-color-fg-on-accent)',
          textDecoration: 'none',
          fontWeight: 500,
        }}
      >
        {t('forbidden.back_home')}
      </Link>
    </section>
  );
}
