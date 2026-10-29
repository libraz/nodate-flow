/**
 * DataSettingsPage — `/workspaces/{wsId}/settings/data`. Hosts two
 * sections: Imports (list + create form + cancel) and Exports (one-shot
 * task export → browser download).
 *
 * Section order is **Exports first, Imports second**: exports is a small
 * single-action card and imports is a longer list-plus-form, so leading
 * with the compact card keeps the page scannable on first paint.
 *
 * There is no CSV preview, column mapping or per-row retry here because
 * the SDK surface cannot drive one: `ImportJobBody` exposes only
 * `{id, source, status, processed/total/failed, errorLog, createdAt,
 * startedAt, completedAt}` — no filename, no preview rows, no mapping.
 * The page offers what the backend supports: a list with progress +
 * cancel, and a minimal create form (source + optional projectId + raw
 * configJson).
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

import { WsErrors } from '@nodate-flow/sdk';
import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Combobox, { type ComboboxOption } from '@nodate-flow/ui/primitives/combobox';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import FormField from '@nodate-flow/ui/primitives/form-field';
import SegmentedControl from '@nodate-flow/ui/primitives/segmented-control';
import Select from '@nodate-flow/ui/primitives/select';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { getRouteApi, Link } from '@tanstack/react-router';
import { type ChangeEvent, type FormEvent, type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ApiError, formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { type Project, useProjectsQuery } from '../projects/api';
import ui from '../settings/settings-ui.module.css';
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
        <div className={styles.progressFill} style={{ scale: `${pct / 100} 1` }} />
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
  cancelling: boolean;
  /** True for a row that arrived after the list first painted. */
  arrived: boolean;
  onCancel: (job: ImportJob) => void;
}

function ImportRow({
  job,
  locale,
  cancelPending,
  cancelling,
  arrived,
  onCancel,
}: ImportRowProps): ReactElement {
  const { t } = useTranslation('settings');
  const isInflight = job.status === 'pending' || job.status === 'running';
  return (
    <tr className={arrived ? ui.rowEnter : undefined}>
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
      <td className={ui.numeric}>{job.failedItems}</td>
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
            aria-busy={cancelling}
          >
            {t('settings.data.imports.cancel')}
          </Button>
        ) : null}
      </td>
    </tr>
  );
}

/**
 * The two ways the server refuses a configuration: a key the source does
 * not define, and a key whose name says it carries a credential. Both
 * are about the configuration field specifically, so both belong on it.
 *
 * The codes come from the generated error module rather than being
 * spelled out here, so renaming one in `errors/ws.yaml` moves this with
 * it instead of quietly turning the branch off.
 */
const CONFIG_REJECTION_CODES: readonly string[] = [
  WsErrors.WS_IMPORT_CONFIG_SECRET_REJECTED.code,
  WsErrors.WS_IMPORT_CONFIG_KEY_UNKNOWN.code,
];

function isConfigRejection(err: unknown): boolean {
  return (
    err instanceof ApiError && err.code !== undefined && CONFIG_REJECTION_CODES.includes(err.code)
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
          const message = formatApiError(err, t, 'settings.data.imports.create.error');
          // A rejected configuration is a problem with one field, and
          // the two rejections ask for different things — drop the key,
          // or move the credential somewhere it belongs. Both land on
          // the field the reader has to edit, where the message stays
          // put while they fix it and the caution above it is still in
          // view, rather than in a toast that leaves.
          if (isConfigRejection(err)) {
            setConfigError(message);
            return;
          }
          toaster.show({ tone: 'danger', message });
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
        description={
          <>
            {t('settings.data.imports.create.config.hint')}
            {/*
              The field is a free-form JSON box on a screen about
              connecting GitHub and Jira, which reads as an invitation to
              paste a personal access token. It is stored in plaintext,
              so say what it is for before someone does. The server
              refuses credentials either way; being told after a failed
              submit is being told once the token is already on a
              clipboard and in a form field.
            */}
            <span className={styles.configCaution}>
              {t('settings.data.imports.create.config.no_credentials')}{' '}
              <Link to="/settings/integrations" className={styles.configCautionLink}>
                {t('settings.data.imports.create.config.credentials_where')}
              </Link>
            </span>
          </>
        }
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
        <Button
          type="submit"
          variant="primary"
          size="sm"
          disabled={submitDisabled}
          aria-busy={create.isPending}
        >
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
  const locale = i18n.resolvedLanguage ?? 'en';
  const importsQuery = useImportsQuery(workspaceId);
  const cancel = useCancelImportMutation();
  const cancellingId = cancel.isPending ? cancel.variables?.importId : undefined;

  const [createOpen, setCreateOpen] = useState(false);
  // Jobs present when the list first loaded; anything else fades in as it arrives.
  const [initialIds, setInitialIds] = useState<ReadonlySet<string> | null>(null);
  if (initialIds === null && importsQuery.data !== undefined) {
    setInitialIds(new Set(importsQuery.data.map((job) => job.id)));
  }

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
    <section className={ui.section} aria-labelledby="data-imports-title">
      <div className={styles.toolbar}>
        <h2 id="data-imports-title" className={ui.sectionTitle}>
          {t('settings.data.imports.title')}
        </h2>
        <Button
          type="button"
          size="sm"
          onClick={() => setCreateOpen((prev) => !prev)}
          aria-expanded={createOpen}
        >
          {t('settings.data.imports.new')}
        </Button>
      </div>

      {createOpen ? (
        <Suspense fallback={<Skeleton className={styles.skeletonForm} />}>
          <ImportCreateForm workspaceId={workspaceId} />
        </Suspense>
      ) : null}

      {importsQuery.isPending ? (
        <div className={ui.skeletonStack} aria-hidden="true">
          <Skeleton className={styles.skeletonRow} />
          <Skeleton className={styles.skeletonRow} />
          <Skeleton className={styles.skeletonRow} />
        </div>
      ) : importsQuery.isError ? (
        <EmptyState
          titleAs="p"
          title={t('settings.data.imports.load_error')}
          className={ui.empty}
          action={
            <Button
              type="button"
              size="sm"
              onClick={() => {
                void importsQuery.refetch();
              }}
              aria-busy={importsQuery.isFetching}
            >
              {t('settings.data.imports.retry')}
            </Button>
          }
        />
      ) : jobs.length === 0 ? (
        <EmptyState titleAs="p" title={t('settings.data.imports.empty')} className={ui.empty} />
      ) : (
        <div className={ui.tableWrap}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th>{t('settings.data.imports.col.source')}</th>
                <th>{t('settings.data.imports.col.status')}</th>
                <th>{t('settings.data.imports.col.progress')}</th>
                <th>{t('settings.data.imports.col.failed')}</th>
                <th>{t('settings.data.imports.col.started')}</th>
                <th>{t('settings.data.imports.col.completed')}</th>
                <th className={ui.alignEnd}>{t('settings.data.imports.col.actions')}</th>
              </tr>
            </thead>
            <tbody>
              {jobs.map((job) => (
                <ImportRow
                  key={job.id}
                  job={job}
                  locale={locale}
                  cancelPending={cancel.isPending}
                  cancelling={cancellingId === job.id}
                  arrived={initialIds !== null && !initialIds.has(job.id)}
                  onCancel={handleCancelImport}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
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

  return (
    <form className={ui.section} onSubmit={handleSubmit} aria-labelledby="data-export-title">
      <div className={ui.sectionHeader}>
        <h2 id="data-export-title" className={ui.sectionTitle}>
          {t('settings.data.export.title')}
        </h2>
        <p className={ui.sectionDescription}>{t('settings.data.export.description')}</p>
      </div>
      <div className={styles.exportRow}>
        <div className={styles.formatGroup}>
          <span className={styles.formatLegend} id="data-export-format">
            {t('settings.data.export.format.label')}
          </span>
          <SegmentedControl<ExportFormat>
            value={format}
            onChange={setFormat}
            options={[
              { value: 'csv', label: t('settings.data.export.format.csv') },
              { value: 'json', label: t('settings.data.export.format.json') },
            ]}
            ariaLabel={t('settings.data.export.format.label')}
          />
        </div>
        <Button
          type="submit"
          variant="primary"
          disabled={exportTasks.isPending}
          aria-busy={exportTasks.isPending}
        >
          {t('settings.data.export.submit')}
        </Button>
      </div>
    </form>
  );
}

/** Page component mounted by the lazy route. */
export default function DataSettingsPage(): ReactElement {
  const { t } = useTranslation('settings');
  const { id } = routeApi.useParams();

  return (
    <div className={ui.page}>
      <header className={ui.header}>
        <div className={ui.headerText}>
          <h1 className={ui.title}>{t('settings.data.title')}</h1>
          <p className={ui.lede}>{t('settings.data.description')}</p>
        </div>
      </header>
      <ExportsCard workspaceId={id} />
      <ImportsCard workspaceId={id} />
    </div>
  );
}
