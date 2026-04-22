/**
 * AuditLogView — workspace audit log table with filters, search,
 * pagination, and CSV export.
 *
 * Renders the most recent audit entries as a compact table. Filter
 * controls are in a collapsible section above the table.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import { type ChangeEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type AuditLogEntry, type AuditLogFilters, useAuditLogsQuery } from './api';

const PAGE_SIZE = 50;

/** Known audit actions for the filter dropdown. */
const ACTION_OPTIONS: readonly string[] = [
  'workspace.created',
  'workspace.updated',
  'project.created',
  'project.updated',
  'task.created',
  'task.updated',
  'task.disabled',
  'auth.login',
  'auth.logout',
];

/** Known resource types for the filter dropdown. */
const RESOURCE_TYPE_OPTIONS: readonly string[] = [
  'workspace',
  'project',
  'task',
  'user',
  'session',
];

function toneForAction(action: string): 'accent' | 'warning' | 'danger' | 'neutral' {
  if (action.startsWith('auth.')) return 'warning';
  if (action.includes('disabled') || action.includes('delete')) return 'danger';
  if (action.includes('created')) return 'accent';
  return 'neutral';
}

function formatTimestamp(unix: number): string {
  return new Date(unix * 1000).toLocaleString();
}

function formatMetadata(meta: Record<string, unknown> | null): string {
  if (!meta) return '';
  return JSON.stringify(meta);
}

function escapeCsvField(value: string): string {
  if (value.includes(',') || value.includes('"') || value.includes('\n')) {
    return `"${value.replace(/"/g, '""')}"`;
  }
  return value;
}

function buildCsv(entries: AuditLogEntry[], headers: readonly string[]): string {
  const headerLine = headers.map(escapeCsvField).join(',');
  const rows = entries.map((e) =>
    [
      formatTimestamp(e.occurredAt),
      e.action,
      e.actorDisplayName ?? '',
      e.resourceType,
      e.resourcePublicId ?? '',
      e.ipAddress ?? '',
      formatMetadata(e.metadataJson),
    ]
      .map(escapeCsvField)
      .join(','),
  );
  return `${headerLine}\n${rows.join('\n')}`;
}

function handleExportCsv(entries: AuditLogEntry[], headers: readonly string[]): void {
  const csv = buildCsv(entries, headers);
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `audit-log-${new Date().toISOString().slice(0, 10)}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}

function AuditRow({ entry }: { entry: AuditLogEntry }): ReactElement {
  const { t } = useTranslation('settings');
  return (
    <tr>
      <td
        style={{
          padding: '0.5rem 0.75rem',
          fontSize: '0.8125rem',
          whiteSpace: 'nowrap',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {formatTimestamp(entry.occurredAt)}
      </td>
      <td style={{ padding: '0.5rem 0.75rem' }}>
        <Badge tone={toneForAction(entry.action)}>{entry.action}</Badge>
      </td>
      <td
        style={{
          padding: '0.5rem 0.75rem',
          fontSize: '0.8125rem',
        }}
      >
        {entry.actorDisplayName ?? t('audit_log.unknown_actor')}
      </td>
      <td
        style={{
          padding: '0.5rem 0.75rem',
          fontSize: '0.8125rem',
        }}
      >
        {entry.resourceType}
      </td>
      <td
        style={{
          padding: '0.5rem 0.75rem',
          fontSize: '0.75rem',
          fontFamily: 'var(--font-mono)',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {entry.resourcePublicId ?? '\u2014'}
      </td>
      <td
        style={{
          padding: '0.5rem 0.75rem',
          fontSize: '0.75rem',
          color: 'var(--nf-color-fg-muted)',
          maxInlineSize: '16rem',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {formatMetadata(entry.metadataJson) || '\u2014'}
      </td>
    </tr>
  );
}

export default function AuditLogView({
  workspaceId,
}: {
  workspaceId: string;
}): ReactElement {
  const { t } = useTranslation('settings');
  const { t: tCommon } = useTranslation('common');
  const weekdayLabels = tCommon('common.date.weekdays', { returnObjects: true }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    tCommon('common.date.monthYear', { year, month });

  const [filters, setFilters] = useState<AuditLogFilters>({
    limit: PAGE_SIZE,
    offset: 0,
  });

  const { data } = useAuditLogsQuery(workspaceId, filters);

  const currentPage = Math.floor((filters.offset ?? 0) / PAGE_SIZE);
  const totalPages = Math.max(1, Math.ceil(data.total / PAGE_SIZE));

  const buildNext = (
    prev: AuditLogFilters,
    overrides: {
      action?: string;
      resourceType?: string;
      actorSearch?: string;
      dateFrom?: string;
      dateTo?: string;
    },
  ): AuditLogFilters => {
    const next: AuditLogFilters = { limit: prev.limit ?? PAGE_SIZE, offset: 0 };
    const m = { ...prev, ...overrides };
    if (m.action) next.action = m.action;
    if (m.resourceType) next.resourceType = m.resourceType;
    if (m.actorSearch) next.actorSearch = m.actorSearch;
    if (m.dateFrom) next.dateFrom = m.dateFrom;
    if (m.dateTo) next.dateTo = m.dateTo;
    return next;
  };

  const handleActionChange = (e: ChangeEvent<HTMLSelectElement>): void => {
    const v = e.target.value;
    setFilters((prev) => buildNext(prev, v ? { action: v } : {}));
  };

  const handleResourceTypeChange = (e: ChangeEvent<HTMLSelectElement>): void => {
    const v = e.target.value;
    setFilters((prev) => buildNext(prev, v ? { resourceType: v } : {}));
  };

  const handleActorSearchChange = (e: ChangeEvent<HTMLInputElement>): void => {
    const v = e.target.value;
    setFilters((prev) => buildNext(prev, v ? { actorSearch: v } : {}));
  };

  const handleDateFromChange = (v: string): void => {
    setFilters((prev) => buildNext(prev, v ? { dateFrom: v } : {}));
  };

  const handleDateToChange = (v: string): void => {
    setFilters((prev) => buildNext(prev, v ? { dateTo: v } : {}));
  };

  const handleClearFilters = (): void => {
    setFilters({ limit: PAGE_SIZE, offset: 0 });
  };

  const handlePrevPage = (): void => {
    setFilters((prev) => ({
      ...prev,
      offset: Math.max(0, (prev.offset ?? 0) - PAGE_SIZE),
    }));
  };

  const handleNextPage = (): void => {
    setFilters((prev) => ({
      ...prev,
      offset: (prev.offset ?? 0) + PAGE_SIZE,
    }));
  };

  const csvHeaders = [
    t('audit_log.column.timestamp'),
    t('audit_log.column.action'),
    t('audit_log.column.actor'),
    t('audit_log.column.resource_type'),
    t('audit_log.column.resource_id'),
    t('audit_log.column.ip_address'),
    t('audit_log.column.metadata'),
  ] as const;

  const handleExport = (): void => {
    handleExportCsv(data.entries, csvHeaders);
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <h1 style={{ margin: 0, fontSize: '1.5rem' }}>{t('audit_log.title')}</h1>
          <Button variant="default" size="sm" onClick={handleExport}>
            {t('audit_log.export_csv')}
          </Button>
        </div>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.875rem' }}>
          {t('audit_log.description')}
        </p>
      </header>

      {/* Filters */}
      <Card style={{ padding: '0.75rem' }}>
        <div
          style={{
            display: 'flex',
            flexWrap: 'wrap',
            gap: '0.75rem',
            alignItems: 'flex-end',
          }}
        >
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
            <label
              htmlFor="audit-action-filter"
              style={{ fontSize: '0.75rem', color: 'var(--nf-color-fg-muted)' }}
            >
              {t('audit_log.filter.action')}
            </label>
            <Select
              id="audit-action-filter"
              value={filters.action ?? ''}
              onChange={handleActionChange}
              style={{ minInlineSize: '12rem' }}
            >
              <option value="">{t('audit_log.filter.all_actions')}</option>
              {ACTION_OPTIONS.map((a) => (
                <option key={a} value={a}>
                  {a}
                </option>
              ))}
            </Select>
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
            <label
              htmlFor="audit-resource-type-filter"
              style={{ fontSize: '0.75rem', color: 'var(--nf-color-fg-muted)' }}
            >
              {t('audit_log.filter.resource_type')}
            </label>
            <Select
              id="audit-resource-type-filter"
              value={filters.resourceType ?? ''}
              onChange={handleResourceTypeChange}
              style={{ minInlineSize: '10rem' }}
            >
              <option value="">{t('audit_log.filter.all_types')}</option>
              {RESOURCE_TYPE_OPTIONS.map((rt) => (
                <option key={rt} value={rt}>
                  {rt}
                </option>
              ))}
            </Select>
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
            <label
              htmlFor="audit-actor-search"
              style={{ fontSize: '0.75rem', color: 'var(--nf-color-fg-muted)' }}
            >
              {t('audit_log.filter.actor_search')}
            </label>
            <Input
              id="audit-actor-search"
              type="search"
              placeholder={t('audit_log.filter.actor_search_placeholder')}
              value={filters.actorSearch ?? ''}
              onChange={handleActorSearchChange}
              style={{ minInlineSize: '12rem' }}
            />
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
            <span style={{ fontSize: '0.75rem', color: 'var(--nf-color-fg-muted)' }}>
              {t('audit_log.filter.date_from')}
            </span>
            <DatePicker
              value={filters.dateFrom ?? ''}
              onChange={handleDateFromChange}
              weekdayLabels={weekdayLabels}
              formatMonthYear={formatMonthYear}
              triggerLabel={filters.dateFrom || tCommon('common.date.placeholder')}
            />
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
            <span style={{ fontSize: '0.75rem', color: 'var(--nf-color-fg-muted)' }}>
              {t('audit_log.filter.date_to')}
            </span>
            <DatePicker
              value={filters.dateTo ?? ''}
              onChange={handleDateToChange}
              weekdayLabels={weekdayLabels}
              formatMonthYear={formatMonthYear}
              triggerLabel={filters.dateTo || tCommon('common.date.placeholder')}
              {...(filters.dateFrom ? { minDate: filters.dateFrom } : {})}
            />
          </div>

          <Button variant="ghost" size="sm" onClick={handleClearFilters}>
            {t('audit_log.filter.clear')}
          </Button>
        </div>
      </Card>

      {/* Table */}
      {data.entries.length === 0 ? (
        <div
          style={{
            padding: '3rem 1rem',
            textAlign: 'center',
            color: 'var(--nf-color-fg-muted, var(--nf-color-fg-muted))',
            border: '1px dashed var(--nf-color-border, var(--nf-color-border))',
            borderRadius: '0.75rem',
            background: 'var(--nf-color-bg-sunken, transparent)',
            fontSize: '0.875rem',
          }}
        >
          {t('audit_log.empty')}
        </div>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table
            style={{
              inlineSize: '100%',
              borderCollapse: 'collapse',
              fontSize: '0.875rem',
            }}
          >
            <thead>
              <tr
                style={{
                  borderBlockEnd: '1px solid var(--nf-color-border)',
                }}
              >
                <th
                  style={{
                    padding: '0.5rem 0.75rem',
                    textAlign: 'start',
                    fontSize: '0.75rem',
                    fontWeight: 500,
                    color: 'var(--nf-color-fg-muted)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.05em',
                  }}
                >
                  {t('audit_log.column.timestamp')}
                </th>
                <th
                  style={{
                    padding: '0.5rem 0.75rem',
                    textAlign: 'start',
                    fontSize: '0.75rem',
                    fontWeight: 500,
                    color: 'var(--nf-color-fg-muted)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.05em',
                  }}
                >
                  {t('audit_log.column.action')}
                </th>
                <th
                  style={{
                    padding: '0.5rem 0.75rem',
                    textAlign: 'start',
                    fontSize: '0.75rem',
                    fontWeight: 500,
                    color: 'var(--nf-color-fg-muted)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.05em',
                  }}
                >
                  {t('audit_log.column.actor')}
                </th>
                <th
                  style={{
                    padding: '0.5rem 0.75rem',
                    textAlign: 'start',
                    fontSize: '0.75rem',
                    fontWeight: 500,
                    color: 'var(--nf-color-fg-muted)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.05em',
                  }}
                >
                  {t('audit_log.column.resource_type')}
                </th>
                <th
                  style={{
                    padding: '0.5rem 0.75rem',
                    textAlign: 'start',
                    fontSize: '0.75rem',
                    fontWeight: 500,
                    color: 'var(--nf-color-fg-muted)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.05em',
                  }}
                >
                  {t('audit_log.column.resource_id')}
                </th>
                <th
                  style={{
                    padding: '0.5rem 0.75rem',
                    textAlign: 'start',
                    fontSize: '0.75rem',
                    fontWeight: 500,
                    color: 'var(--nf-color-fg-muted)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.05em',
                  }}
                >
                  {t('audit_log.column.metadata')}
                </th>
              </tr>
            </thead>
            <tbody>
              {data.entries.map((entry) => (
                <AuditRow key={entry.publicId} entry={entry} />
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {data.total > PAGE_SIZE ? (
        <nav
          aria-label={t('audit_log.pagination.label')}
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            gap: '1rem',
          }}
        >
          <Button variant="ghost" size="sm" disabled={currentPage === 0} onClick={handlePrevPage}>
            {t('audit_log.pagination.prev')}
          </Button>
          <span style={{ fontSize: '0.8125rem', color: 'var(--nf-color-fg-muted)' }}>
            {t('audit_log.pagination.page_of', {
              current: currentPage + 1,
              total: totalPages,
            })}
          </span>
          <Button
            variant="ghost"
            size="sm"
            disabled={currentPage >= totalPages - 1}
            onClick={handleNextPage}
          >
            {t('audit_log.pagination.next')}
          </Button>
        </nav>
      ) : null}
    </div>
  );
}
