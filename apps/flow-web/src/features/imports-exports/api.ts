/**
 * Imports & Exports feature — typed react-query hooks for the workspace
 * data import/export endpoints.
 *
 * Surfaces:
 *   - {@link useImportsQuery}              GET    /workspaces/{wsId}/imports
 *   - {@link useCreateImportMutation}      POST   /workspaces/{wsId}/imports
 *   - {@link useCancelImportMutation}      POST   /workspaces/{wsId}/imports/{importId}/cancel
 *   - {@link useExportTasksMutation}       GET    /workspaces/{wsId}/export/tasks
 *
 * The list query intentionally uses {@link useQuery} (not the suspense
 * variant) because the page polls it while a job is running so a row's
 * progress can update without throwing the whole pane back into the
 * Suspense fallback every refetch.
 *
 * The export endpoint returns a JSON envelope `{count, format, tasks}`
 * regardless of the `?format=` query parameter — the server echoes the
 * format in the response body but always emits `application/json`. The
 * mutation therefore reads the JSON body and synthesises a CSV/JSON Blob
 * client-side before triggering a download via a transient anchor
 * element.
 *
 * Row ceiling
 * -----------
 * The export operation takes `format`, `lensId`, and `limit` — there is
 * no `offset` or `cursor`, and the server clamps `limit` to
 * {@link EXPORT_MAX_ROWS}. A workspace larger than that ceiling therefore
 * cannot be exported in full from this client at all: paging is not
 * expressible against the endpoint. The mutation asks for the ceiling and
 * reports {@link ExportTasksResult.truncated} so callers can say the file
 * is partial instead of calling an incomplete backup a success.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';

import { type ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

/** Import job DTO mirrored from the generated SDK. */
export type ImportJob = components['schemas']['ImportJobBody'];

/** Allowed `source` values for {@link useCreateImportMutation}. */
export type ImportSource = 'github' | 'jira' | 'linear' | 'csv';

/** Allowed export formats for {@link useExportTasksMutation}. */
export type ExportFormat = 'csv' | 'json';

/** Single exported task row, mirrored from the generated SDK. */
type ExportedTask = components['schemas']['ExportedTask'];

/** Query key factory for the imports-exports feature. */
export const importsKeys = {
  all: ['imports'] as const,
  list: (wsId: string) => ['imports', wsId, 'list'] as const,
};

/**
 * GET /workspaces/{wsId}/imports — list import jobs for the workspace.
 *
 * Returns a non-suspense {@link UseQueryResult} so the page can render an
 * inline spinner while polling and surface refetch errors without
 * tearing down the whole pane.
 */
export interface UseImportsQueryOptions {
  limit?: number;
  offset?: number;
}

export function useImportsQuery(
  wsId: string,
  opts: UseImportsQueryOptions = {},
): UseQueryResult<ImportJob[], ApiError> {
  const limit = opts.limit ?? 100;
  const offset = opts.offset ?? 0;
  return useQuery<ImportJob[], ApiError>({
    queryKey: [...importsKeys.list(wsId), limit, offset],
    queryFn: async (): Promise<ImportJob[]> => {
      if (!wsId) return [];
      const { data, error } = await sdk.GET('/workspaces/{wsId}/imports', {
        params: { path: { wsId }, query: { limit, offset } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load import jobs');
      return data.items ?? [];
    },
  });
}

/** Arguments for {@link useCreateImportMutation}. */
export interface CreateImportArgs {
  wsId: string;
  body: {
    source: ImportSource;
    projectId?: string;
    configJson?: Record<string, unknown>;
  };
}

/**
 * POST /workspaces/{wsId}/imports — start a new import job. Invalidates
 * the workspace's import list so the new row appears immediately.
 */
export function useCreateImportMutation(): UseMutationResult<
  ImportJob,
  ApiError,
  CreateImportArgs
> {
  const qc = useQueryClient();
  return useMutation<ImportJob, ApiError, CreateImportArgs>({
    mutationFn: async ({ wsId, body }): Promise<ImportJob> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/imports', {
        params: { path: { wsId } },
        body: {
          source: body.source,
          ...(body.projectId != null ? { projectId: body.projectId } : {}),
          ...(body.configJson != null ? { configJson: body.configJson } : {}),
        },
      });
      if (error || !data) throw toApiError(error, 'Failed to create import job');
      return data;
    },
    onSuccess: (_data, { wsId }) => {
      void qc.invalidateQueries({ queryKey: ['imports', wsId] });
    },
  });
}

/** Arguments for {@link useCancelImportMutation}. */
export interface CancelImportArgs {
  wsId: string;
  importId: string;
}

/**
 * POST /workspaces/{wsId}/imports/{importId}/cancel — request cancellation
 * of an in-flight import. The backend may still take a moment to flip
 * the status to `cancelled`, so we invalidate the list and let the
 * polling cadence pick up the new state.
 */
export function useCancelImportMutation(): UseMutationResult<void, ApiError, CancelImportArgs> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, CancelImportArgs>({
    mutationFn: async ({ wsId, importId }): Promise<void> => {
      const { error } = await sdk.POST('/workspaces/{wsId}/imports/{importId}/cancel', {
        params: { path: { wsId, importId } },
      });
      if (error) throw toApiError(error, 'Failed to cancel import job');
    },
    onSuccess: (_data, { wsId }) => {
      void qc.invalidateQueries({ queryKey: ['imports', wsId] });
    },
  });
}

/**
 * Hard ceiling the export endpoint enforces on `limit`. Asking for more
 * is rejected by request validation, and the handler clamps to this value
 * anyway, so it is the largest export a single request can produce.
 */
export const EXPORT_MAX_ROWS = 10_000;

/** Arguments for {@link useExportTasksMutation}. */
export interface ExportTasksArgs {
  wsId: string;
  format: ExportFormat;
  lensId?: string;
  /** Row ceiling for this request. Defaults to {@link EXPORT_MAX_ROWS}. */
  limit?: number;
}

/** Result emitted by {@link useExportTasksMutation} on success. */
export interface ExportTasksResult {
  count: number;
  format: ExportFormat;
  filename: string;
  /**
   * The response filled the requested row ceiling, so the workspace
   * almost certainly holds tasks the file does not. Exactly-at-the-limit
   * exports read as truncated too — over-warning costs a sentence, while
   * under-warning hands someone an incomplete backup they trust.
   */
  truncated: boolean;
}

/** Columns emitted in the synthesised CSV, in order. */
const CSV_COLUMNS = [
  'id',
  'title',
  'description',
  'projectId',
  'projectName',
  'assigneeId',
  'assigneeDisplayName',
  'status',
  'priority',
  'dueOn',
  'startedOn',
  'completedAt',
  'createdAt',
] as const;

type CsvColumn = (typeof CSV_COLUMNS)[number];

/**
 * Format a single task field as a CSV cell value. Numeric fields stringify
 * as decimals; missing optionals render as empty strings.
 */
function csvValueOf(task: ExportedTask, key: CsvColumn): string {
  const value: unknown = task[key as keyof ExportedTask];
  if (value === undefined || value === null) return '';
  if (typeof value === 'number') return String(value);
  if (typeof value === 'string') return value;
  return String(value);
}

/**
 * Escape a CSV cell per RFC 4180: quote when the value contains a delimiter,
 * a double quote, or a line break, and double up internal quotes.
 */
function escapeCsvCell(value: string): string {
  if (value.includes(',') || value.includes('"') || value.includes('\n') || value.includes('\r')) {
    return `"${value.replace(/"/g, '""')}"`;
  }
  return value;
}

/** Serialise the exported tasks into an RFC 4180 CSV string. */
function buildCsv(tasks: readonly ExportedTask[]): string {
  const header = CSV_COLUMNS.join(',');
  const rows = tasks.map((task) =>
    CSV_COLUMNS.map((col) => escapeCsvCell(csvValueOf(task, col))).join(','),
  );
  return `${header}\n${rows.join('\n')}\n`;
}

/**
 * UTF-8 byte order mark. Excel reads a BOM-less CSV as the machine's
 * legacy code page, which turns every Japanese and Chinese title into
 * mojibake. Spelled out as bytes because a U+FEFF string literal is
 * invisible in an editor and easy to lose in a later edit; this way the
 * blob provably carries the same three octets the server's CSV route
 * writes ahead of its own output.
 */
const UTF8_BOM = Uint8Array.from([0xef, 0xbb, 0xbf]);

/** Wrap a CSV string in a BOM-prefixed, Excel-readable Blob. */
function csvBlob(csv: string): Blob {
  return new Blob([UTF8_BOM, csv], { type: 'text/csv;charset=utf-8' });
}

/** Build a stable `YYYYMMDDHHmm` timestamp for download filenames. */
function buildTimestamp(): string {
  const now = new Date();
  const yyyy = String(now.getFullYear());
  const mm = String(now.getMonth() + 1).padStart(2, '0');
  const dd = String(now.getDate()).padStart(2, '0');
  const hh = String(now.getHours()).padStart(2, '0');
  const mi = String(now.getMinutes()).padStart(2, '0');
  return `${yyyy}${mm}${dd}${hh}${mi}`;
}

/**
 * Trigger a browser download for the given Blob using a transient anchor
 * element. The object URL is revoked on the next macrotask so the browser
 * has time to start the download.
 */
function triggerDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  anchor.style.display = 'none';
  document.body.appendChild(anchor);
  anchor.click();
  setTimeout(() => {
    URL.revokeObjectURL(url);
    anchor.remove();
  }, 100);
}

/**
 * GET /workspaces/{wsId}/export/tasks — fetch the workspace's tasks,
 * synthesise a CSV or JSON file client-side, and trigger a browser
 * download. Resolves with the row count, format, filename, and whether
 * the response hit the row ceiling so callers can surface a toast.
 *
 * `limit` is always sent explicitly. Leaving it off falls back to the
 * server's 5000-row default, which silently halves the ceiling the
 * endpoint can actually serve — the difference between a partial file and
 * a complete one for most workspaces that hit it at all.
 *
 * A truncated export is named `tasks-export-partial-…` so the file keeps
 * saying it is incomplete long after the toast is gone.
 */
export function useExportTasksMutation(): UseMutationResult<
  ExportTasksResult,
  ApiError,
  ExportTasksArgs
> {
  return useMutation<ExportTasksResult, ApiError, ExportTasksArgs>({
    mutationFn: async ({ wsId, format, lensId, limit }): Promise<ExportTasksResult> => {
      const rowLimit = limit ?? EXPORT_MAX_ROWS;
      const { data, error } = await sdk.GET('/workspaces/{wsId}/export/tasks', {
        params: {
          path: { wsId },
          query: {
            format,
            ...(lensId != null ? { lensId } : {}),
            limit: rowLimit,
          },
        },
      });
      if (error || !data) throw toApiError(error, 'Failed to export tasks');

      const tasks: ExportedTask[] = data.tasks ?? [];
      const truncated = tasks.length >= rowLimit;
      const timestamp = buildTimestamp();
      const stem = truncated ? `tasks-export-partial-${timestamp}` : `tasks-export-${timestamp}`;

      if (format === 'csv') {
        const filename = `${stem}.csv`;
        triggerDownload(csvBlob(buildCsv(tasks)), filename);
        return { count: tasks.length, format: 'csv', filename, truncated };
      }

      // No BOM on the JSON branch: a leading U+FEFF is not valid JSON
      // text and trips strict parsers, and the mojibake problem it solves
      // is specific to Excel opening CSV.
      const json = JSON.stringify(tasks, null, 2);
      const filename = `${stem}.json`;
      triggerDownload(new Blob([json], { type: 'application/json;charset=utf-8' }), filename);
      return { count: tasks.length, format: 'json', filename, truncated };
    },
  });
}
