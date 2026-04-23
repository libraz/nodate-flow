/**
 * /admin/audit-logs -- Instance audit log viewer with filters and pagination.
 */

import Button from '@nodate-flow/ui/primitives/button';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Input from '@nodate-flow/ui/primitives/input';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { sdk } from '../../../lib/sdk';

interface AuditLogEntry {
  id: string;
  action: string;
  actorEmail: string;
  targetType: string;
  targetId: string;
  workspaceName: string | null;
  ipAddress: string;
  occurredAt: number;
}

interface AuditLogsResponse {
  items: AuditLogEntry[];
  total: number;
}

const tableStyle: React.CSSProperties = {
  width: '100%',
  borderCollapse: 'collapse',
  fontSize: 'var(--nf-text-sm, 0.875rem)',
};

const thStyle: React.CSSProperties = {
  textAlign: 'start',
  padding: 'var(--nf-space-2, 0.5rem) var(--nf-space-3, 0.75rem)',
  borderBlockEnd: '2px solid var(--nf-color-border)',
  fontWeight: 600,
  color: 'var(--nf-color-fg-muted)',
};

const tdStyle: React.CSSProperties = {
  padding: 'var(--nf-space-2, 0.5rem) var(--nf-space-3, 0.75rem)',
  borderBlockEnd: '1px solid var(--nf-color-border)',
};

function formatTimestamp(ts: number): string {
  return new Date(ts * 1000).toLocaleString();
}

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

    const params = new URLSearchParams();
    params.set('page', String(page));
    params.set('perPage', String(perPage));
    if (actionFilter) params.set('action', actionFilter);
    if (fromDate) params.set('from', fromDate);
    if (toDate) params.set('to', toDate);

    void sdk
      .GET('/admin/audit-logs', {
        params: {
          query: {
            page: String(page),
            perPage: String(perPage),
            ...(actionFilter ? { action: actionFilter } : {}),
            ...(fromDate ? { from: fromDate } : {}),
            ...(toDate ? { to: toDate } : {}),
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
        setEntries(body.items);
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
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6, 1.5rem)' }}>
      <h1
        style={{
          fontFamily: 'var(--nf-font-display, var(--font-display))',
          fontSize: 'var(--nf-text-2xl, 1.5rem)',
          margin: 0,
        }}
      >
        {t('audit_logs.title')}
      </h1>

      <div
        style={{
          display: 'flex',
          gap: 'var(--nf-space-3, 0.75rem)',
          alignItems: 'center',
          flexWrap: 'wrap',
        }}
      >
        <div style={{ flex: 1, minWidth: '200px' }}>
          <Input
            type="text"
            placeholder={t('audit_logs.filter_action')}
            value={actionFilter}
            onChange={handleActionChange}
          />
        </div>
        <div
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1, 0.25rem)' }}
        >
          <span
            style={{
              fontSize: 'var(--nf-text-xs, 0.75rem)',
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
        <div
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1, 0.25rem)' }}
        >
          <span
            style={{
              fontSize: 'var(--nf-text-xs, 0.75rem)',
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
            color: 'var(--nf-color-danger)',
            fontSize: 'var(--nf-text-sm, 0.875rem)',
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
          <table style={tableStyle}>
            <thead>
              <tr>
                <th style={thStyle}>{t('audit_logs.occurred_at')}</th>
                <th style={thStyle}>{t('audit_logs.action')}</th>
                <th style={thStyle}>{t('audit_logs.actor')}</th>
                <th style={thStyle}>{t('audit_logs.target')}</th>
                <th style={thStyle}>{t('audit_logs.ip_address')}</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((entry) => (
                <tr key={entry.id}>
                  <td style={tdStyle}>{formatTimestamp(entry.occurredAt)}</td>
                  <td style={tdStyle}>{entry.action}</td>
                  <td style={tdStyle}>{entry.actorEmail}</td>
                  <td style={tdStyle}>
                    {entry.targetType}
                    {entry.workspaceName ? ` (${entry.workspaceName})` : ''}
                  </td>
                  <td style={tdStyle}>{entry.ipAddress}</td>
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
          fontSize: 'var(--nf-text-sm, 0.875rem)',
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
