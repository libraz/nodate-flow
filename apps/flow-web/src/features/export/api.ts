/**
 * Export feature — mutation hook that fetches task exports in CSV or JSON
 * format and triggers a browser download via an invisible anchor element.
 *
 * Uses raw fetch (not the SDK) because the CSV response is not JSON and
 * the SDK's openapi-fetch client expects typed JSON envelopes.
 */

import { type UseMutationResult, useMutation } from '@tanstack/react-query';

import { apiBaseUrl } from '../../lib/sdk';
import { authStore } from '../auth/auth-store';

/** Supported export formats. */
export type ExportFormat = 'csv' | 'json';

/** Arguments for the export mutation. */
export interface ExportTasksArgs {
  workspaceId: string;
  format: ExportFormat;
  /** Optional lens ID to scope the export to a saved view. */
  lensId?: string | undefined;
}

/** Lightweight error thrown when the export API call fails. */
export class ExportApiError extends Error {
  readonly code: string | undefined;
  constructor(code: string | undefined, message: string) {
    super(message);
    this.name = 'ExportApiError';
    this.code = code;
  }
}

function toError(err: unknown, fallback: string): ExportApiError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    return new ExportApiError(code, message);
  }
  return new ExportApiError(undefined, fallback);
}

function authHeaders(): HeadersInit {
  const token = authStore.getState().accessToken;
  // biome-ignore lint/style/useNamingConvention: HTTP header name
  return token ? { Authorization: `Bearer ${token}` } : {};
}

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
export function useExportTasks(): UseMutationResult<number, ExportApiError, ExportTasksArgs> {
  return useMutation<number, ExportApiError, ExportTasksArgs>({
    mutationFn: async ({ workspaceId, format, lensId }): Promise<number> => {
      const params = new URLSearchParams({ format, limit: '5000' });
      if (lensId) {
        params.set('lensId', lensId);
      }
      const url = `${apiBaseUrl}/workspaces/${workspaceId}/export/tasks?${params.toString()}`;

      const res = await fetch(url, {
        credentials: 'include',
        headers: authHeaders(),
      });

      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as unknown;
        throw toError(body, `Export failed with status ${String(res.status)}`);
      }

      const timestamp = buildTimestamp();

      if (format === 'csv') {
        const text = await res.text();
        const blob = new Blob([text], { type: 'text/csv;charset=utf-8' });
        triggerDownload(blob, `tasks-${timestamp}.csv`);
        // Count rows (subtract header row)
        const lines = text.split('\n').filter((line) => line.trim().length > 0);
        return Math.max(0, lines.length - 1);
      }

      // JSON format
      const data = (await res.json()) as { count?: number; tasks?: unknown[] };
      const jsonStr = JSON.stringify(data, null, 2);
      const blob = new Blob([jsonStr], { type: 'application/json;charset=utf-8' });
      triggerDownload(blob, `tasks-${timestamp}.json`);
      return data.count ?? data.tasks?.length ?? 0;
    },
  });
}
