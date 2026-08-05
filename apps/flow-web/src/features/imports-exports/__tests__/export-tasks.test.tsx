/**
 * Task export coverage — row ceiling, file encoding, and the messaging
 * that tells the user which of the two they got.
 *
 * The export operation accepts `format`, `lensId`, and `limit` but no
 * offset or cursor, so "fetch the rest" is not expressible: the only
 * defence against handing someone a partial backup is to ask for the
 * ceiling and say so when the response fills it. The SDK mock below
 * mimics the server on both counts — it applies the same 5000-row
 * default when `limit` is omitted and the same 10000-row clamp — so a
 * regression that drops the explicit limit shows up as missing rows
 * rather than as a passing test.
 */

import type { components } from '@nodate-flow/sdk';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, renderHook, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18next from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

type ExportedTask = components['schemas']['ExportedTask'];

/* ── mocks ────────────────────────────────────────────────────── */

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  toastShow: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  // openapi-fetch keys its client by HTTP method, hence the caps.
  sdk: { GET: mocks.get, POST: mocks.post },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

// The page reads only its `wsId` path param from the route.
vi.mock('@tanstack/react-router', () => ({
  getRouteApi: () => ({ useParams: () => ({ id: 'ws-1' }) }),
}));

import { EXPORT_MAX_ROWS, useExportTasksMutation } from '../api';
import DataSettingsPage from '../data-settings-page';

/* ── fixtures ─────────────────────────────────────────────────── */

/** The handler's fallback when the request omits `limit`. */
const SERVER_DEFAULT_LIMIT = 5_000;

function exportedTasks(count: number): ExportedTask[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `task-${i}`,
    title: `Task ${i}`,
    status: 'open',
    priority: 2,
    projectId: 'prj-1',
    projectName: 'Alpha',
    createdAt: 1_700_000_000,
  })) as ExportedTask[];
}

/**
 * Stand in for the export handler: serve at most `limit` rows out of a
 * workspace holding `datasetSize`, defaulting and clamping exactly as
 * the server does.
 */
function serveDataset(datasetSize: number): void {
  mocks.get.mockImplementation(
    async (
      _path: string,
      init: { params?: { query?: { limit?: number; format?: string } } },
    ): Promise<unknown> => {
      const query = init.params?.query ?? {};
      const requested = typeof query.limit === 'number' ? query.limit : SERVER_DEFAULT_LIMIT;
      const limit = Math.min(requested, EXPORT_MAX_ROWS);
      const tasks = exportedTasks(Math.min(datasetSize, limit));
      return { data: { count: tasks.length, format: query.format, tasks }, error: null };
    },
  );
}

/* ── download capture ─────────────────────────────────────────── */

interface CapturedDownload {
  blob: Blob;
}

const downloads: CapturedDownload[] = [];

function captureDownloads(): void {
  downloads.length = 0;
  // happy-dom has no object-URL implementation; the stub doubles as the
  // seam for inspecting the bytes the browser would have saved.
  globalThis.URL.createObjectURL = vi.fn((blob: Blob): string => {
    downloads.push({ blob });
    return `blob:mock-${downloads.length}`;
  }) as unknown as typeof URL.createObjectURL;
  globalThis.URL.revokeObjectURL = vi.fn() as unknown as typeof URL.revokeObjectURL;
}

/* ── provider wrappers ────────────────────────────────────────── */

function buildClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
}

function hookWrapper(client: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

/**
 * The page test needs a real resource bundle rather than the usual
 * key-passthrough: the point under test is that an error code is
 * resolved through the `errors` namespace instead of the server's raw
 * English detail reaching the toast, which is only observable when the
 * lookup can actually succeed.
 */
function buildPageI18n(): ReturnType<typeof i18next.createInstance> {
  const instance = i18next.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'common',
      ns: ['common', 'settings', 'errors'],
      resources: {
        en: {
          common: {},
          settings: {
            settings: {
              data: {
                export: {
                  submit: 'Export',
                  success: 'Exported {count} tasks',
                  truncated: 'Only the first {count, number} rows; not a complete backup',
                  error: 'Fallback export failure',
                },
              },
            },
          },
          errors: { 'EXPORT.TASK.DATASET_QUERY_FAILED': 'Localized dataset failure' },
        },
      },
      interpolation: { escapeValue: false },
      parseMissingKeyHandler: (key: string, defaultValue?: string) =>
        defaultValue !== undefined ? defaultValue : key,
      react: { useSuspense: false },
    });
  return instance;
}

function renderPage(): void {
  const client = buildClient();
  const instance = buildPageI18n();
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={instance}>
        <DataSettingsPage />
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mocks.get.mockReset();
  mocks.post.mockReset();
  mocks.toastShow.mockReset();
  captureDownloads();
});

/* ── tests ────────────────────────────────────────────────────── */

describe('useExportTasksMutation — row ceiling', () => {
  it('asks for the endpoint ceiling instead of letting the server default apply', async () => {
    serveDataset(10);
    const { result } = renderHook(() => useExportTasksMutation(), {
      wrapper: hookWrapper(buildClient()),
    });

    await result.current.mutateAsync({ wsId: 'ws-1', format: 'csv' });

    const query = mocks.get.mock.calls[0]?.[1]?.params?.query as Record<string, unknown>;
    expect(query.limit).toBe(EXPORT_MAX_ROWS);
    expect(EXPORT_MAX_ROWS).toBe(10_000);
  });

  it('exports every row of a workspace larger than the server default', async () => {
    serveDataset(6_000);
    const { result } = renderHook(() => useExportTasksMutation(), {
      wrapper: hookWrapper(buildClient()),
    });

    const outcome = await result.current.mutateAsync({ wsId: 'ws-1', format: 'csv' });

    // Omitting the limit would have stopped at SERVER_DEFAULT_LIMIT and
    // still reported success.
    expect(outcome.count).toBe(6_000);
    expect(outcome.count).toBeGreaterThan(SERVER_DEFAULT_LIMIT);
    expect(outcome.truncated).toBe(false);
    expect(outcome.filename).not.toContain('partial');
  });

  it('reports truncation and marks the filename when the ceiling is filled', async () => {
    serveDataset(12_000);
    const { result } = renderHook(() => useExportTasksMutation(), {
      wrapper: hookWrapper(buildClient()),
    });

    const outcome = await result.current.mutateAsync({ wsId: 'ws-1', format: 'csv' });

    expect(outcome.count).toBe(EXPORT_MAX_ROWS);
    expect(outcome.truncated).toBe(true);
    expect(outcome.filename).toContain('tasks-export-partial-');
  });
});

describe('useExportTasksMutation — file encoding', () => {
  it('prefixes the CSV with a UTF-8 BOM so Excel does not mangle CJK titles', async () => {
    serveDataset(2);
    const { result } = renderHook(() => useExportTasksMutation(), {
      wrapper: hookWrapper(buildClient()),
    });

    await result.current.mutateAsync({ wsId: 'ws-1', format: 'csv' });

    const blob = downloads[0]?.blob;
    expect(blob).toBeDefined();
    const bytes = new Uint8Array(await (blob as Blob).arrayBuffer());
    expect(Array.from(bytes.slice(0, 3))).toEqual([0xef, 0xbb, 0xbf]);

    // The BOM is a prefix, not a replacement: the header row still leads
    // the payload.
    const text = await (blob as Blob).text();
    expect(text.codePointAt(0)).toBe(0xfeff);
    expect(text.slice(1).startsWith('id,title,description')).toBe(true);
  });

  it('leaves JSON exports BOM-free, since a leading U+FEFF is not valid JSON', async () => {
    serveDataset(2);
    const { result } = renderHook(() => useExportTasksMutation(), {
      wrapper: hookWrapper(buildClient()),
    });

    await result.current.mutateAsync({ wsId: 'ws-1', format: 'json' });

    const blob = downloads[0]?.blob;
    const text = await (blob as Blob).text();
    expect(text.codePointAt(0)).not.toBe(0xfeff);
    expect(() => JSON.parse(text) as unknown).not.toThrow();
  });
});

describe('DataSettingsPage — export outcome messaging', () => {
  it('warns instead of congratulating when the export was truncated', async () => {
    const user = userEvent.setup();
    serveDataset(12_000);
    renderPage();

    await user.click(screen.getByRole('button', { name: 'Export' }));

    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalled();
    });
    const toast = mocks.toastShow.mock.calls[0]?.[0] as { tone: string; message: string };
    expect(toast.tone).toBe('warning');
    expect(toast.message).toContain('not a complete backup');
    expect(toast.message).toContain('10,000');
  });

  it('congratulates only when the whole workspace fit in the file', async () => {
    const user = userEvent.setup();
    serveDataset(42);
    renderPage();

    await user.click(screen.getByRole('button', { name: 'Export' }));

    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalled();
    });
    const toast = mocks.toastShow.mock.calls[0]?.[0] as { tone: string; message: string };
    expect(toast.tone).toBe('success');
    expect(toast.message).toBe('Exported 42 tasks');
  });

  it('shows the localized error for a failed export, not the server detail', async () => {
    const user = userEvent.setup();
    mocks.get.mockResolvedValue({
      data: undefined,
      error: {
        type: 'EXPORT.TASK.DATASET_QUERY_FAILED',
        detail: 'dataset query failed',
        status: 500,
      },
    });
    renderPage();

    await user.click(screen.getByRole('button', { name: 'Export' }));

    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalled();
    });
    const toast = mocks.toastShow.mock.calls[0]?.[0] as { tone: string; message: string };
    expect(toast.tone).toBe('danger');
    expect(toast.message).toBe('Localized dataset failure');
    expect(toast.message).not.toBe('dataset query failed');
  });
});
