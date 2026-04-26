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

/** Arguments for {@link useExportTasksMutation}. */
export interface ExportTasksArgs {
  wsId: string;
  format: ExportFormat;
  lensId?: string;
  limit?: number;
}

/** Result emitted by {@link useExportTasksMutation} on success. */
export interface ExportTasksResult {
  count: number;
  format: ExportFormat;
  filename: string;
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
 * GET /workspaces/{wsId}/export/tasks — fetch every task in the workspace,
 * synthesise a CSV or JSON file client-side, and trigger a browser
 * download. Resolves with the row count, format, and filename so callers
 * can surface a toast.
 */
export function useExportTasksMutation(): UseMutationResult<
  ExportTasksResult,
  ApiError,
  ExportTasksArgs
> {
  return useMutation<ExportTasksResult, ApiError, ExportTasksArgs>({
    mutationFn: async ({ wsId, format, lensId, limit }): Promise<ExportTasksResult> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/export/tasks', {
        params: {
          path: { wsId },
          query: {
            format,
            ...(lensId != null ? { lensId } : {}),
            ...(limit != null ? { limit } : {}),
          },
        },
      });
      if (error || !data) throw toApiError(error, 'Failed to export tasks');

      const tasks: ExportedTask[] = data.tasks ?? [];
      const timestamp = buildTimestamp();

      if (format === 'csv') {
        const csv = buildCsv(tasks);
        const filename = `tasks-export-${timestamp}.csv`;
        triggerDownload(new Blob([csv], { type: 'text/csv;charset=utf-8' }), filename);
        return { count: tasks.length, format: 'csv', filename };
      }

      const json = JSON.stringify(tasks, null, 2);
      const filename = `tasks-export-${timestamp}.json`;
      triggerDownload(new Blob([json], { type: 'application/json;charset=utf-8' }), filename);
      return { count: tasks.length, format: 'json', filename };
    },
  });
}
