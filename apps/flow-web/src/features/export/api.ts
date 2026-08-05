/**
 * Export feature — mutation hook that fetches task exports in CSV or JSON
 * format and triggers a browser download via an invisible anchor element.
 *
 * Goes through the typed `@nodate-flow/sdk`. Each format has its own
 * route: the CSV download is a documented operation returning
 * `text/csv`, and the JSON export returns its envelope. Neither branch
 * has to reach past the SDK for the raw response body any more.
 */

import { type UseMutationResult, useMutation } from '@tanstack/react-query';

import { ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

/** Supported export formats. */
export type ExportFormat = 'csv' | 'json';

/** Arguments for the export mutation. */
export interface ExportTasksArgs {
  workspaceId: string;
  format: ExportFormat;
  /** Optional lens ID to scope the export to a saved view. */
  lensId?: string | undefined;
}

export { ApiError as ExportApiError };

/**
 * Triggers a file download by creating an invisible anchor, clicking it,
 * then revoking the object URL. Uses `useRef`-free approach since the
 * anchor is ephemeral and immediately removed.
 */
function triggerDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  anchor.style.display = 'none';
  document.body.appendChild(anchor);
  anchor.click();
  // Clean up after a tick to ensure the browser has started the download.
  setTimeout(() => {
    URL.revokeObjectURL(url);
    anchor.remove();
  }, 100);
}

/**
 * Header carrying the number of task rows in a CSV download. The file
 * cannot report its own row count: a description containing a newline
 * makes a line tally wrong.
 */
const EXPORT_ROW_COUNT_HEADER = 'X-Export-Row-Count';

function buildTimestamp(): string {
  const now = new Date();
  const y = String(now.getFullYear());
  const m = String(now.getMonth() + 1).padStart(2, '0');
  const d = String(now.getDate()).padStart(2, '0');
  return `${y}${m}${d}`;
}

/**
 * useExportTasks — mutation that downloads a task export and reports how
 * many tasks it contained.
 *
 * The CSV branch calls the CSV route. It used to call the JSON route
 * with `format=csv`, read the body as text and save it as a `.csv` —
 * which meant the downloaded "CSV" was a JSON document wearing a CSV
 * extension, because that route only ever returned JSON no matter what
 * `format` said. The row count was then derived by splitting on
 * newlines, which is wrong for any task whose description contains one.
 * The server sends both the file and its row count now.
 */
export function useExportTasks(): UseMutationResult<number, ApiError, ExportTasksArgs> {
  return useMutation<number, ApiError, ExportTasksArgs>({
    mutationFn: async ({ workspaceId, format, lensId }): Promise<number> => {
      const query = {
        limit: 5000,
        ...(lensId !== undefined ? { lensId } : {}),
      };
      const timestamp = buildTimestamp();

      if (format === 'csv') {
        const { data, error, response } = await sdk.GET('/workspaces/{wsId}/export/tasks.csv', {
          params: { path: { wsId: workspaceId }, query },
          parseAs: 'blob',
        });
        if (error) {
          throw toApiError(error, 'Export failed');
        }
        if (!response.ok || !(data instanceof Blob)) {
          throw new ApiError(undefined, `Export failed with status ${String(response.status)}`);
        }
        triggerDownload(data, `tasks-${timestamp}.csv`);
        const count = Number.parseInt(response.headers.get(EXPORT_ROW_COUNT_HEADER) ?? '', 10);
        return Number.isNaN(count) ? 0 : count;
      }

      const { data, error, response } = await sdk.GET('/workspaces/{wsId}/export/tasks', {
        params: { path: { wsId: workspaceId }, query: { format: 'json', ...query } },
      });
      if (error) {
        throw toApiError(error, 'Export failed');
      }
      if (!response.ok || !data) {
        throw new ApiError(undefined, `Export failed with status ${String(response.status)}`);
      }

      const jsonStr = JSON.stringify(data, null, 2);
      triggerDownload(
        new Blob([jsonStr], { type: 'application/json;charset=utf-8' }),
        `tasks-${timestamp}.json`,
      );
      return data.count ?? data.tasks?.length ?? 0;
    },
  });
}
