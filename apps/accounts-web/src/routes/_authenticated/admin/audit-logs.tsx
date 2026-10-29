/**
 * /admin/audit-logs -- Instance audit log viewer with filters and pagination.
 */

import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Input from '@nodate-flow/ui/primitives/input';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import TableRegion from '../../../components/table-region';
import { apiRequest } from '../../../lib/api';
import { formatTimestamp } from '../../../lib/format-timestamp';

import styles from './audit-logs.module.css';

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
  const actionFilterId = useId();
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

    void apiRequest(
      (client) =>
        client.GET('/admin/audit-logs', {
          params: {
            query: {
              limit: perPage,
              offset,
              ...(actionFilter ? { action: actionFilter } : {}),
              ...(fromTs !== undefined ? { from: fromTs } : {}),
              ...(toTs !== undefined ? { to: toTs } : {}),
            },
          },
        }),
      'Failed to load audit logs',
      { onError: 'empty', empty: null },
    ).then((result) => {
      if (cancelled) return;
      if (result === null) {
        setError(t('errors.generic'));
        setLoading(false);
        return;
      }
      const body = result as AuditLogsResponse;
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
    <div className="aw-stack aw-stack-6">
      <h1 className="aw-page-title">{t('audit_logs.title')}</h1>

      <div className={styles.filters}>
        <div className={styles.filter}>
          <label htmlFor={actionFilterId} className="aw-muted aw-text-xs">
            {t('audit_logs.filter_action')}
          </label>
          <Input
            id={actionFilterId}
            type="text"
            placeholder={t('audit_logs.filter_action')}
            value={actionFilter}
            onChange={handleActionChange}
          />
        </div>
        <div className="aw-stack aw-stack-1">
          <span className="aw-muted aw-text-xs">{t('audit_logs.filter_from')}</span>
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
        <div className="aw-stack aw-stack-1">
          <span className="aw-muted aw-text-xs">{t('audit_logs.filter_to')}</span>
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
        <p role="alert" className="aw-error">
          {error}
        </p>
      ) : null}

      <TableRegion
        loading={loading}
        rowCount={entries.length}
        loadingLabel={t('common.loading')}
        emptyTitle={t('audit_logs.no_results')}
      >
        <table className="aw-table">
          <thead>
            <tr>
              <th className="aw-th">{t('audit_logs.occurred_at')}</th>
              <th className="aw-th">{t('audit_logs.action')}</th>
              <th className="aw-th">{t('audit_logs.actor')}</th>
              <th className="aw-th">{t('audit_logs.target')}</th>
              <th className="aw-th">{t('audit_logs.ip_address')}</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((entry) => (
              <tr key={entry.id}>
                <td className="aw-td">{formatTimestamp(entry.occurredAt, { locale })}</td>
                <td className="aw-td">{entry.action}</td>
                <td className="aw-td">{entry.actorDisplayName ?? ''}</td>
                <td className="aw-td">
                  {entry.targetResourceType ?? ''}
                  {entry.targetWorkspaceName ? ` (${entry.targetWorkspaceName})` : ''}
                </td>
                <td className="aw-td">{entry.ipAddress ?? ''}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>

      <div className="aw-pagination">
        <Button variant="default" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
          {t('common.previous')}
        </Button>
        <span className="aw-muted">{t('common.page', { page, total: totalPages })}</span>
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
