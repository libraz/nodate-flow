/**
 * DataSettingsPage — `/workspaces/{wsId}/settings/data`. Hosts two
 * sections: Imports (list + create form + cancel) and Exports (one-shot
 * task export → browser download).
 *
 * Section order is **Exports first, Imports second**: exports is a small
 * single-action card and imports is a longer list-plus-form, so leading
 * with the compact card keeps the page scannable on first paint.
 *
 * Deviation from the original W7 plan: the plan called for a CSV preview
 * + column-mapping + retry wizard. The actual SDK surface
 * (`ImportJobBody`) only exposes `{id, source, status, processed/total/
 * failed, errorLog, createdAt, startedAt, completedAt}` — no filename,
 * no preview rows, no mapping, no per-row retry. We ship what the
 * backend can actually drive: a list with progress + cancel, and a
 * minimal create form (source + optional projectId + raw configJson).
 *
 * The Exports endpoint always returns a JSON envelope regardless of the
 * `?format=` query value — it does not stream `text/csv`. The export
 * mutation in `./api.ts` consequently fetches the JSON body and
 * synthesises a CSV/JSON Blob client-side before triggering the
 * download. That endpoint has a row ceiling and no way to page past it,
 * so an export that fills the ceiling reports itself as truncated and
 * this page surfaces it as a warning rather than a success.
 *
 * Hooks consumed (all in `./api.ts`):
 *   - {@link useImportsQuery}              — list (poll-friendly useQuery)
 *   - {@link useCreateImportMutation}      — POST imports
 *   - {@link useCancelImportMutation}      — POST imports/{id}/cancel
 *   - {@link useExportTasksMutation}       — GET export/tasks + Blob download
 */

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Combobox, { type ComboboxOption } from '@nodate-flow/ui/primitives/combobox';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Select from '@nodate-flow/ui/primitives/select';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { getRouteApi } from '@tanstack/react-router';
import { type ChangeEvent, type FormEvent, type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { type Project, useProjectsQuery } from '../projects/api';
import {
  type ExportFormat,
  type ImportJob,
  type ImportSource,
  useCancelImportMutation,
  useCreateImportMutation,
  useExportTasksMutation,
  useImportsQuery,
} from './api';
import styles from './data-settings-page.module.css';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/data');

const IMPORT_SOURCES: readonly ImportSource[] = ['github', 'jira', 'linear', 'csv'];

/** Map raw ImportJob.status to a Badge tone. Unknown statuses fall back to neutral. */
function statusTone(status: string): BadgeTone {
  switch (status) {
    case 'pending':
    case 'running':
      return 'info';
    case 'completed':
      return 'success';
    case 'failed':
      return 'danger';
    case 'cancelled':
      return 'neutral';
    default:
      return 'neutral';
  }
}

/** Resolve the i18n key for a status pill label. */
function statusLabelKey(
  status: string,
):
  | 'settings.data.imports.status.pending'
  | 'settings.data.imports.status.running'
  | 'settings.data.imports.status.completed'
  | 'settings.data.imports.status.failed'
  | 'settings.data.imports.status.cancelled' {
  switch (status) {
    case 'running':
      return 'settings.data.imports.status.running';
    case 'completed':
      return 'settings.data.imports.status.completed';
    case 'failed':
      return 'settings.data.imports.status.failed';
    case 'cancelled':
      return 'settings.data.imports.status.cancelled';
    default:
      return 'settings.data.imports.status.pending';
  }
}

/** Resolve the i18n key for a source pill label. */
function sourceLabelKey(
  source: string,
):
  | 'settings.data.imports.source.github'
  | 'settings.data.imports.source.jira'
  | 'settings.data.imports.source.linear'
  | 'settings.data.imports.source.csv' {
  switch (source) {
    case 'github':
      return 'settings.data.imports.source.github';
    case 'jira':
      return 'settings.data.imports.source.jira';
    case 'linear':
      return 'settings.data.imports.source.linear';
    default:
      return 'settings.data.imports.source.csv';
  }
}

/** Format a unix-second timestamp using the active locale's medium date+short time. */
function formatTimestamp(epochSec: number | undefined, locale: string): string {
  if (!epochSec) return '—';
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(
      new Date(epochSec * 1000),
    );
  } catch {
    return String(epochSec);
  }
}

/** Sort jobs newest-first by createdAt. */
function sortByCreatedDesc(jobs: readonly ImportJob[]): ImportJob[] {
  return [...jobs].sort((a, b) => b.createdAt - a.createdAt);
}

interface ProgressBarProps {
  processed: number;
  total: number;
}

/** Slim progress bar: `processed / total` numerals + a thin filled track. */
function ProgressBar({ processed, total }: ProgressBarProps): ReactElement {
  const safeTotal = total > 0 ? total : 0;
  const pct = safeTotal > 0 ? Math.min(100, Math.round((processed / safeTotal) * 100)) : 0;
  return (
    <div className={styles.progress}>
      <div
        className={styles.progressBar}
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={safeTotal || 1}
        aria-valuenow={processed}
      >
        <div className={styles.progressFill} style={{ inlineSize: `${pct}%` }} />
      </div>
      <span className={styles.progressLabel}>
        {processed} / {safeTotal}
      </span>
    </div>
  );
}

interface ImportRowProps {
  job: ImportJob;
  locale: string;
  cancelPending: boolean;
  onCancel: (job: ImportJob) => void;
}

function ImportRow({ job, locale, cancelPending, onCancel }: ImportRowProps): ReactElement {
  const { t } = useTranslation('settings');
  const isInflight = job.status === 'pending' || job.status === 'running';
  return (
    <tr>
      <td>
        <Badge tone="neutral">{t(sourceLabelKey(job.source))}</Badge>
      </td>
      <td>
        <Badge tone={statusTone(job.status)}>{t(statusLabelKey(job.status))}</Badge>
        {job.status === 'failed' && job.errorLog ? (
          <details className={styles.errorLog}>
            <summary>{t('settings.data.imports.error_log')}</summary>
            <pre className={styles.errorLogBody}>{job.errorLog}</pre>
          </details>
        ) : null}
      </td>
      <td>
        <ProgressBar processed={job.processedItems} total={job.totalItems} />
      </td>
      <td>{job.failedItems}</td>
      <td className={styles.timestamp}>{formatTimestamp(job.startedAt, locale)}</td>
      <td className={styles.timestamp}>{formatTimestamp(job.completedAt, locale)}</td>
      <td className={styles.actionsCell}>
        {isInflight ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => onCancel(job)}
            disabled={cancelPending}
          >
            {t('settings.data.imports.cancel')}
          </Button>
        ) : null}
      </td>
    </tr>
  );
}

interface ImportCreateFormProps {
  workspaceId: string;
}

/**
 * Suspense-boundary-ed create form. Wrapped separately because the
 * project Combobox is fed by `useProjectsQuery`, a suspense query — we
 * keep the surrounding list rendered while the project list loads.
 */
function ImportCreateForm({ workspaceId }: ImportCreateFormProps): ReactElement {
  const { t } = useTranslation('settings');
  const { data: projects } = useProjectsQuery(workspaceId);
  const create = useCreateImportMutation();

  const [source, setSource] = useState<ImportSource>('csv');
  const [projectId, setProjectId] = useState<string>('');
  const [configJsonText, setConfigJsonText] = useState<string>('');
  const [configError, setConfigError] = useState<string | null>(null);

  const projectOptions: ComboboxOption[] = [
    { value: '', label: t('settings.data.imports.create.project.placeholder') },
    ...projects.map((p: Project) => ({
      value: p.id,
      label: p.identifier ? `${p.name} (${p.identifier})` : p.name,
    })),
  ];

  const validateConfig = (text: string): { ok: boolean; parsed?: Record<string, unknown> } => {
    const trimmed = text.trim();
    if (trimmed.length === 0) return { ok: true };
    try {
      const parsed = JSON.parse(trimmed) as unknown;
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        return { ok: true, parsed: parsed as Record<string, unknown> };
      }
      return { ok: false };
    } catch {
      return { ok: false };
    }
  };

  const handleConfigBlur = (): void => {
    const result = validateConfig(configJsonText);
    setConfigError(result.ok ? null : t('settings.data.imports.create.config.invalid'));
  };

  const handleConfigChange = (event: ChangeEvent<HTMLTextAreaElement>): void => {
    setConfigJsonText(event.target.value);
    if (configError) {
      // Clear the error as soon as the user resumes typing so the
      // submit button can re-enable when the JSON becomes valid again.
      setConfigError(null);
    }
  };

  const handleSourceChange = (event: ChangeEvent<HTMLSelectElement>): void => {
    setSource(event.target.value as ImportSource);
  };

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const config = validateConfig(configJsonText);
    if (!config.ok) {
      setConfigError(t('settings.data.imports.create.config.invalid'));
      return;
    }

    const body: {
      source: ImportSource;
      projectId?: string;
      configJson?: Record<string, unknown>;
    } = { source };
    if (projectId) body.projectId = projectId;
    if (config.parsed) body.configJson = config.parsed;

    create.mutate(
      { wsId: workspaceId, body },
      {
        onSuccess: () => {
          toaster.show({
            tone: 'success',
            message: t('settings.data.imports.create.success'),
          });
          setProjectId('');
          setConfigJsonText('');
          setConfigError(null);
        },
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'settings.data.imports.create.error'),
          });
        },
      },
    );
  };

  const submitDisabled = create.isPending || configError !== null;

  return (
    <form className={styles.createForm} onSubmit={handleSubmit}>
      <div className={styles.createGrid}>
        <FormField label={t('settings.data.imports.create.source')}>
          {(control) => (
            <Select {...control} value={source} onChange={handleSourceChange}>
              {IMPORT_SOURCES.map((s) => (
                <option key={s} value={s}>
                  {t(sourceLabelKey(s))}
                </option>
              ))}
            </Select>
          )}
        </FormField>
        <FormField label={t('settings.data.imports.create.project.label')}>
          {(control) => (
            <Combobox
              {...control}
              value={projectId}
              onChange={setProjectId}
              options={projectOptions}
              placeholder={t('settings.data.imports.create.project.placeholder')}
              aria-label={t('settings.data.imports.create.project.label')}
            />
          )}
        </FormField>
      </div>
      <FormField
        label={t('settings.data.imports.create.config.label')}
        description={t('settings.data.imports.create.config.hint')}
        {...(configError ? { error: configError } : {})}
      >
        {(control) => (
          <Textarea
            {...control}
            className={styles.configTextarea}
            value={configJsonText}
            onChange={handleConfigChange}
            onBlur={handleConfigBlur}
            placeholder={'{\n  "key": "value"\n}'}
            rows={5}
            invalid={configError !== null}
          />
        )}
      </FormField>
      <div className={styles.createActions}>
        <Button type="submit" variant="primary" size="sm" disabled={submitDisabled}>
          {t('settings.data.imports.create.submit')}
        </Button>
      </div>
    </form>
  );
}

interface ImportsCardProps {
  workspaceId: string;
}

function ImportsCard({ workspaceId }: ImportsCardProps): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const { t: tCommon } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  const importsQuery = useImportsQuery(workspaceId);
  const cancel = useCancelImportMutation();

  const [createOpen, setCreateOpen] = useState(false);

  const handleCancelImport = (job: ImportJob): void => {
    void (async (): Promise<void> => {
      const ok = await confirmAction({
        title: t('settings.data.imports.cancel'),
        message: t('settings.data.imports.cancel_confirm'),
        confirmLabel: t('settings.data.imports.cancel'),
        tone: 'danger',
      });
      if (!ok) return;
      cancel.mutate(
        { wsId: workspaceId, importId: job.id },
        {
          onSuccess: () => {
            toaster.show({
              tone: 'success',
              message: t('settings.data.imports.cancel_success'),
            });
          },
          onError: (err) => {
            toaster.show({
              tone: 'danger',
              message: formatApiError(err, t, 'settings.data.imports.cancel_error'),
            });
          },
        },
      );
    })();
  };

  const jobs = sortByCreatedDesc(importsQuery.data ?? []);

  return (
    <Card>
      <section className={styles.section}>
        <div className={styles.toolbar}>
          <h2 className={styles.sectionTitle}>{t('settings.data.imports.title')}</h2>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setCreateOpen((prev) => !prev)}
            aria-expanded={createOpen}
          >
            {t('settings.data.imports.new')}
          </Button>
        </div>

        {createOpen ? (
          <Suspense
            fallback={
              <div className={styles.spinnerRow}>
                <Spinner label={tCommon('common.loading')} />
              </div>
            }
          >
            <ImportCreateForm workspaceId={workspaceId} />
          </Suspense>
        ) : null}

        {importsQuery.isLoading ? (
          <div className={styles.spinnerRow}>
            <Spinner label={tCommon('common.loading')} />
          </div>
        ) : jobs.length === 0 ? (
          <p className={styles.empty}>{t('settings.data.imports.empty')}</p>
        ) : (
          <div className={styles.tableWrap}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>{t('settings.data.imports.col.source')}</th>
                  <th>{t('settings.data.imports.col.status')}</th>
                  <th>{t('settings.data.imports.col.progress')}</th>
                  <th>{t('settings.data.imports.col.failed')}</th>
                  <th>{t('settings.data.imports.col.started')}</th>
                  <th>{t('settings.data.imports.col.completed')}</th>
                  <th className={styles.actionsCell}>{t('settings.data.imports.col.actions')}</th>
                </tr>
              </thead>
              <tbody>
                {jobs.map((job) => (
                  <ImportRow
                    key={job.id}
                    job={job}
                    locale={locale}
                    cancelPending={cancel.isPending}
                    onCancel={handleCancelImport}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </Card>
  );
}

interface ExportsCardProps {
  workspaceId: string;
}

/**
 * The truncation warning carries a consequence ("this is not a complete
 * backup"), which takes longer to read and act on than a confirmation.
 */
const TRUNCATED_TOAST_DURATION_MS = 12_000;

function ExportsCard({ workspaceId }: ExportsCardProps): ReactElement {
  const { t } = useTranslation('settings');
  const exportTasks = useExportTasksMutation();
  const [format, setFormat] = useState<ExportFormat>('csv');

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    exportTasks.mutate(
      { wsId: workspaceId, format },
      {
        onSuccess: ({ count, truncated }) => {
          // A file that stops at the row ceiling is not the backup the
          // button offered, so it does not get to look like one.
          if (truncated) {
            toaster.show({
              tone: 'warning',
              message: t('settings.data.export.truncated', { count }),
              duration: TRUNCATED_TOAST_DURATION_MS,
            });
            return;
          }
          toaster.show({
            tone: 'success',
            message: t('settings.data.export.success', { count }),
          });
        },
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'settings.data.export.error'),
          });
        },
      },
    );
  };

  const handleFormatChange = (event: ChangeEvent<HTMLInputElement>): void => {
    setFormat(event.target.value as ExportFormat);
  };

  return (
    <Card>
      <form className={styles.section} onSubmit={handleSubmit}>
        <h2 className={styles.sectionTitle}>{t('settings.data.export.title')}</h2>
        <p className={styles.sectionDescription}>{t('settings.data.export.description')}</p>
        <div className={styles.exportRow}>
          <fieldset className={styles.formatGroup}>
            <legend className={styles.formatLegend}>
              {t('settings.data.export.format.label')}
            </legend>
            <div className={styles.formatChoices}>
              <label className={styles.formatLabel}>
                <input
                  type="radio"
                  name="export-format"
                  value="csv"
                  checked={format === 'csv'}
                  onChange={handleFormatChange}
                />
                {t('settings.data.export.format.csv')}
              </label>
              <label className={styles.formatLabel}>
                <input
                  type="radio"
                  name="export-format"
                  value="json"
                  checked={format === 'json'}
                  onChange={handleFormatChange}
                />
                {t('settings.data.export.format.json')}
              </label>
            </div>
          </fieldset>
          <Button type="submit" variant="primary" size="sm" disabled={exportTasks.isPending}>
            {t('settings.data.export.submit')}
          </Button>
        </div>
      </form>
    </Card>
  );
}

/** Page component mounted by the lazy route. */
export default function DataSettingsPage(): ReactElement {
  const { t } = useTranslation('settings');
  const { id } = routeApi.useParams();

  return (
    <section className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>{t('settings.data.title')}</h1>
        <p className={styles.description}>{t('settings.data.description')}</p>
      </div>
      <ExportsCard workspaceId={id} />
      <ImportsCard workspaceId={id} />
    </section>
  );
}
