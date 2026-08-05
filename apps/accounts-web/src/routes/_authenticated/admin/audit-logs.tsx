/**
 * /admin/audit-logs -- Instance audit log viewer with filters and pagination.
 */

import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Input from '@nodate-flow/ui/primitives/input';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { adminTableStyle, adminTdStyle, adminThStyle } from '../../../features/admin/styles';
import { formatTimestamp } from '../../../lib/format-timestamp';
import { sdk } from '../../../lib/sdk';

/**
 * SDK-derived shapes; the local interfaces this replaced silently drifted
 * from the API and rendered `undefined` for actor/target columns.
 */
type AuditLogEntry = components['schemas']['AuditEntry'];
type AuditLogsResponse = components['schemas']['ListAuditLogsOutputBody'];

/** Format an ISO `YYYY-MM-DD` string using the given BCP 47 locale. */
function formatIsoDate(iso: string, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(
      new Date(`${iso}T00:00`),
    );
  } catch {
    return iso;
  }
}

function AuditLogsPage(): ReactElement {
  const { t } = useTranslation('admin');
  const { t: tCommon, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  const weekdayLabels = tCommon('common.date.weekdays', { returnObjects: true }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    tCommon('common.date.monthYear', { year, month });
  const [entries, setEntries] = useState<AuditLogEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [perPage] = useState(20);
  const [actionFilter, setActionFilter] = useState('');
  const [fromDate, setFromDate] = useState('');
  const [toDate, setToDate] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);

    // The audit-logs endpoint accepts unix-seconds bounds; the date pickers
    // emit YYYY-MM-DD which we convert to the local-midnight epoch so the
    // filter matches the user's wall clock.
    const toUnix = (iso: string): number | undefined => {
      if (!iso) return undefined;
      const ts = new Date(`${iso}T00:00`).getTime();
      return Number.isFinite(ts) ? Math.floor(ts / 1000) : undefined;
    };
    const fromTs = toUnix(fromDate);
    const toTs = toUnix(toDate);
    const offset = (page - 1) * perPage;

    void sdk
      .GET('/admin/audit-logs', {
        params: {
          query: {
            limit: perPage,
            offset,
            ...(actionFilter ? { action: actionFilter } : {}),
            ...(fromTs !== undefined ? { from: fromTs } : {}),
            ...(toTs !== undefined ? { to: toTs } : {}),
          },
        },
      })
      .then((result) => {
        if (cancelled) return;
        if (result.error || !result.data) {
          setError(t('errors.generic'));
          setLoading(false);
          return;
        }
        const body = result.data as AuditLogsResponse;
        setEntries(body.items ?? []);
        setTotal(body.total);
        setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [page, perPage, actionFilter, fromDate, toDate, t]);

  const totalPages = Math.max(1, Math.ceil(total / perPage));

  const handleActionChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setActionFilter(e.target.value);
    setPage(1);
  };

  const handleFromChange = (value: string) => {
    setFromDate(value);
    setPage(1);
  };

  const handleToChange = (value: string) => {
    setToDate(value);
    setPage(1);
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6)' }}>
      <h1
        style={{
          fontFamily: 'var(--nf-font-sans)',
          fontSize: 'var(--nf-text-2xl)',
          margin: 0,
        }}
      >
        {t('audit_logs.title')}
      </h1>

      <div
        style={{
          display: 'flex',
          gap: 'var(--nf-space-3)',
          alignItems: 'center',
          flexWrap: 'wrap',
        }}
      >
        {/* nf-token-override: component dimension, not a spacing step */}
        <div style={{ flex: 1, minWidth: '200px' }}>
          <Input
            type="text"
            placeholder={t('audit_logs.filter_action')}
            value={actionFilter}
            onChange={handleActionChange}
          />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1)' }}>
          <span
            style={{
              fontSize: 'var(--nf-text-xs)',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('audit_logs.filter_from')}
          </span>
          <DatePicker
            value={fromDate}
            onChange={handleFromChange}
            weekdayLabels={weekdayLabels}
            formatMonthYear={formatMonthYear}
            prevLabel={tCommon('common.date.prev_month')}
            nextLabel={tCommon('common.date.next_month')}
            triggerLabel={
              fromDate ? formatIsoDate(fromDate, locale) : tCommon('common.date.placeholder')
            }
          />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1)' }}>
          <span
            style={{
              fontSize: 'var(--nf-text-xs)',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('audit_logs.filter_to')}
          </span>
          <DatePicker
            value={toDate}
            onChange={handleToChange}
            weekdayLabels={weekdayLabels}
            formatMonthYear={formatMonthYear}
            prevLabel={tCommon('common.date.prev_month')}
            nextLabel={tCommon('common.date.next_month')}
            triggerLabel={
              toDate ? formatIsoDate(toDate, locale) : tCommon('common.date.placeholder')
            }
            {...(fromDate ? { minDate: fromDate } : {})}
          />
        </div>
      </div>

      {error ? (
        <p
          role="alert"
          style={{
            margin: 0,
            color: 'var(--nf-color-danger-fg)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          {error}
        </p>
      ) : null}

      {loading ? (
        <p style={{ color: 'var(--nf-color-fg-muted)' }}>{t('common.loading')}</p>
      ) : entries.length === 0 ? (
        <p style={{ color: 'var(--nf-color-fg-muted)' }}>{t('audit_logs.no_results')}</p>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={adminTableStyle}>
            <thead>
              <tr>
                <th style={adminThStyle}>{t('audit_logs.occurred_at')}</th>
                <th style={adminThStyle}>{t('audit_logs.action')}</th>
                <th style={adminThStyle}>{t('audit_logs.actor')}</th>
                <th style={adminThStyle}>{t('audit_logs.target')}</th>
                <th style={adminThStyle}>{t('audit_logs.ip_address')}</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((entry) => (
                <tr key={entry.id}>
                  <td style={adminTdStyle}>{formatTimestamp(entry.occurredAt, { locale })}</td>
                  <td style={adminTdStyle}>{entry.action}</td>
                  <td style={adminTdStyle}>{entry.actorDisplayName ?? ''}</td>
                  <td style={adminTdStyle}>
                    {entry.targetResourceType ?? ''}
                    {entry.targetWorkspaceName ? ` (${entry.targetWorkspaceName})` : ''}
                  </td>
                  <td style={adminTdStyle}>{entry.ipAddress ?? ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          fontSize: 'var(--nf-text-sm)',
        }}
      >
        <Button variant="default" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
          {t('common.previous')}
        </Button>
        <span style={{ color: 'var(--nf-color-fg-muted)' }}>
          {t('common.page', { page, total: totalPages })}
        </span>
        <Button
          variant="default"
          disabled={page >= totalPages}
          onClick={() => setPage((p) => p + 1)}
        >
          {t('common.next')}
        </Button>
      </div>
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin/audit-logs')({
  component: AuditLogsPage,
});
