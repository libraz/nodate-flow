/**
 * Export feature — mutation hook that fetches task exports in CSV or JSON
 * format and triggers a browser download via an invisible anchor element.
 *
 * Goes through the typed `@nodate-flow/sdk`. The SDK types the response
 * as JSON (the OpenAPI spec only documents the JSON envelope) but we
 * pass `parseAs: 'stream'` and read the raw `Response` so the CSV branch
 * can pull the body as text. The JSON branch decodes through
 * `Response.json()` for the same reason.
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

function buildTimestamp(): string {
  const now = new Date();
  const y = String(now.getFullYear());
  const m = String(now.getMonth() + 1).padStart(2, '0');
  const d = String(now.getDate()).padStart(2, '0');
  return `${y}${m}${d}`;
}

/**
 * useExportTasks — mutation that fetches the export endpoint and triggers
 * a browser download. Returns the number of exported tasks on success.
 */
export function useExportTasks(): UseMutationResult<number, ApiError, ExportTasksArgs> {
  return useMutation<number, ApiError, ExportTasksArgs>({
    mutationFn: async ({ workspaceId, format, lensId }): Promise<number> => {
      // `parseAs: 'stream'` short-circuits the SDK's default JSON decode
      // so we can pull the raw response body — required for the CSV
      // branch and also useful for the JSON branch (we want the parsed
      // envelope, not the typed-but-wrong `Body` schema generated from
      // the OpenAPI spec).
      const { error, response } = await sdk.GET('/workspaces/{wsId}/export/tasks', {
        params: {
          path: { wsId: workspaceId },
          query: {
            format,
            limit: 5000,
            ...(lensId !== undefined ? { lensId } : {}),
          },
        },
        parseAs: 'stream',
      });

      if (error) {
        throw toApiError(error, 'Export failed');
      }
      if (!response.ok) {
        throw new ApiError(undefined, `Export failed with status ${String(response.status)}`);
      }

      const timestamp = buildTimestamp();

      if (format === 'csv') {
        const text = await response.text();
        const blob = new Blob([text], { type: 'text/csv;charset=utf-8' });
        triggerDownload(blob, `tasks-${timestamp}.csv`);
        // Count rows (subtract header row).
        const lines = text.split('\n').filter((line) => line.trim().length > 0);
        return Math.max(0, lines.length - 1);
      }

      // JSON format.
      const data = (await response.json()) as { count?: number; tasks?: unknown[] };
      const jsonStr = JSON.stringify(data, null, 2);
      const blob = new Blob([jsonStr], { type: 'application/json;charset=utf-8' });
      triggerDownload(blob, `tasks-${timestamp}.json`);
      return data.count ?? data.tasks?.length ?? 0;
    },
  });
}
