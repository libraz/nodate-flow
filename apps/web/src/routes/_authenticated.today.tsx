/**
 * /today — minimal "Today" view. Placeholder until the real today-feed
 * feature ships; renders a heading and an empty state so the sidebar
 * Today entry has a real destination.
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

function TodayRoute(): ReactElement {
  const { t } = useTranslation('common');
  return (
    <section
      style={{
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        maxInlineSize: '60rem',
        marginInline: 'auto',
        inlineSize: '100%',
      }}
    >
      <h1
        style={{
          margin: 0,
          fontFamily: 'var(--font-display)',
          fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
        }}
      >
        {t('today.title')}
      </h1>
      <p style={{ margin: 0, color: 'var(--color-muted)' }}>{t('today.empty')}</p>
    </section>
  );
}

export const Route = createFileRoute('/_authenticated/today')({
  component: TodayRoute,
});
