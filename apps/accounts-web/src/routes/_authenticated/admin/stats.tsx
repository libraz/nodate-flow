/**
 * /admin/stats -- Instance statistics dashboard.
 *
 * A deliberately understated page: two KPI tiles for users + workspaces,
 * a refresh button, a "last refreshed" timestamp, and a placeholder
 * panel hinting at future metrics. We use `useQuery` (not Suspense)
 * so the layout stays in place while data loads or refreshes, which
 * matches the controlled feel of an admin dashboard.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  useInstanceStatsQuery,
  useInvalidateInstanceStats,
} from '../../../features/admin-stats/api';
import KpiCard from '../../../features/admin-stats/kpi-card';
import PlaceholderSection from '../../../features/admin-stats/placeholder-section';

/**
 * Resolves a locale string suitable for `Intl.DateTimeFormat` from the
 * active i18n language. We map the short codes accounts-web uses
 * (`zh`) to a fully-qualified BCP 47 tag so number / date formatting
 * matches the UI language.
 */
function resolveLocale(lang: string | undefined): string {
  if (!lang) return 'en';
  if (lang.startsWith('zh')) return 'zh-CN';
  if (lang.startsWith('ja')) return 'ja-JP';
  return lang;
}

function formatLastUpdated(ts: number | null, locale: string): string {
  if (ts === null) return '—';
  return new Intl.DateTimeFormat(locale, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(ts));
}

function StatsPage(): ReactElement {
  const { t, i18n } = useTranslation('instanceStats');
  const query = useInstanceStatsQuery();
  const invalidate = useInvalidateInstanceStats();
  const [lastUpdated, setLastUpdated] = useState<number | null>(null);

  // Sync the "last refreshed" timestamp with the query's own data
  // arrival event. This is a legitimate Effect: we are syncing UI
  // state to a value owned by an external system (the query cache).
  useEffect(() => {
    if (query.dataUpdatedAt > 0) {
      setLastUpdated(query.dataUpdatedAt);
    }
  }, [query.dataUpdatedAt]);

  const locale = resolveLocale(i18n.language);
  const data = query.data;
  const isBusy = query.isPending || query.isFetching;

  const handleRefresh = (): void => {
    void invalidate();
  };

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-6, 1.5rem)',
      }}
    >
      <header
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1, 0.25rem)' }}
      >
        <h1
          style={{
            fontFamily: 'var(--nf-font-display, var(--font-display))',
            fontSize: 'var(--nf-text-2xl, 1.5rem)',
            margin: 0,
          }}
        >
          {t('page.title')}
        </h1>
        <p
          style={{
            margin: 0,
            color: 'var(--nf-color-fg-muted)',
            fontSize: 'var(--nf-text-sm, 0.875rem)',
          }}
        >
          {t('page.subtitle')}
        </p>
      </header>

      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          flexWrap: 'wrap',
          gap: 'var(--nf-space-3, 0.75rem)',
        }}
      >
        <output
          aria-live="polite"
          style={{
            color: 'var(--nf-color-fg-muted)',
            fontSize: 'var(--nf-text-xs, 0.75rem)',
            fontVariantNumeric: 'tabular-nums',
          }}
        >
          {t('actions.lastUpdated', { time: formatLastUpdated(lastUpdated, locale) })}
        </output>
        <Button type="button" variant="default" disabled={isBusy} onClick={handleRefresh}>
          {isBusy ? t('actions.refreshing') : t('actions.refresh')}
        </Button>
      </div>

      {query.isError ? (
        <div
          role="alert"
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 'var(--nf-space-3, 0.75rem)',
            padding: 'var(--nf-space-4, 1rem)',
            borderRadius: 'var(--nf-radius-md, 0.375rem)',
            border: '1px solid var(--nf-color-border)',
            background: 'color-mix(in srgb, var(--nf-color-danger, red) 10%, transparent)',
            color: 'var(--nf-color-danger, red)',
            fontSize: 'var(--nf-text-sm, 0.875rem)',
          }}
        >
          <span>{t('error.fetchFailed')}</span>
          <Button type="button" variant="default" onClick={handleRefresh}>
            {t('error.retry')}
          </Button>
        </div>
      ) : null}

      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))',
          gap: 'var(--nf-space-4, 1rem)',
        }}
      >
        <KpiCard
          title={t('kpi.users.title')}
          help={t('kpi.users.help')}
          value={data?.totalUsers}
          loading={isBusy}
        />
        <KpiCard
          title={t('kpi.workspaces.title')}
          help={t('kpi.workspaces.help')}
          value={data?.totalWorkspaces}
          loading={isBusy}
        />
      </div>

      <PlaceholderSection title={t('placeholder.title')} body={t('placeholder.body')} />
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin/stats')({
  component: StatsPage,
});
