import { CalendarErrors } from '@nodate-flow/sdk';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18next from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import enCommon from '../../../../locales/en/common.json';

const mocks = vi.hoisted(() => ({
  role: 'manager' as string,
  labels: [] as Array<{ id: string; name: string; color: string }>,
  listError: false,
  writeError: undefined as unknown,
  calls: [] as Array<{ method: string; body?: unknown; labelId?: string }>,
  toastShow: vi.fn(),
  confirm: vi.fn(),
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({ toaster: { show: mocks.toastShow } }));
vi.mock('../../../lib/confirm-action', () => ({ confirmAction: mocks.confirm }));
vi.mock('../api', () => ({
  useCalendarQuery: () => ({ data: { role: mocks.role } }),
}));
vi.mock('../../../lib/api', () => {
  const client = {
    GET: async () => {
      if (mocks.listError) throw new Error('boom');
      return { labels: mocks.labels };
    },
    POST: async (_path: string, init: { body: unknown }) => {
      mocks.calls.push({ method: 'POST', body: init.body });
      if (mocks.writeError) throw mocks.writeError;
      return { id: 'new', ...(init.body as object) };
    },
    PATCH: async (
      _path: string,
      init: { body: unknown; params: { path: { labelId: string } } },
    ) => {
      mocks.calls.push({ method: 'PATCH', body: init.body, labelId: init.params.path.labelId });
      if (mocks.writeError) throw mocks.writeError;
      return {};
    },
    DELETE: async (_path: string, init: { params: { path: { labelId: string } } }) => {
      mocks.calls.push({ method: 'DELETE', labelId: init.params.path.labelId });
      if (mocks.writeError) throw mocks.writeError;
    },
  };
  return { apiRequest: (fn: (c: typeof client) => unknown) => fn(client) };
});

import { ApiError } from '../../../lib/api-error';
import LabelsTab from '../labels-tab';

function renderTab(): { qc: QueryClient; invalidate: ReturnType<typeof vi.spyOn> } {
  const i18n = i18next.createInstance();
  void i18n
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'common',
      ns: ['common'],
      resources: { en: { common: enCommon } },
      interpolation: { escapeValue: false },
    });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const invalidate = vi.spyOn(qc, 'invalidateQueries');
  const ui: ReactElement = (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <LabelsTab workspaceId="ws" calendarId="cal" />
      </QueryClientProvider>
    </I18nextProvider>
  );
  render(ui);
  return { qc, invalidate };
}

const sampleLabels = [
  { id: 'l1', name: 'Client visit', color: '#E73B3B' },
  { id: 'l2', name: 'Focus', color: '#2ECC87' },
];

function invalidatedKeys(spy: ReturnType<typeof vi.spyOn>): unknown[] {
  return spy.mock.calls.map((c: unknown[]) => (c[0] as { queryKey: unknown }).queryKey);
}

beforeEach(() => {
  mocks.role = 'manager';
  mocks.labels = sampleLabels.map((l) => ({ ...l }));
  mocks.listError = false;
  mocks.writeError = undefined;
  mocks.calls = [];
  mocks.toastShow.mockReset();
  mocks.confirm.mockReset();
});

describe('LabelsTab list', () => {
  it('shows the rows and management buttons to a manager', async () => {
    renderTab();
    expect(await screen.findByText('Client visit')).toBeTruthy();
    expect(screen.getByTestId('label-count').textContent).toBe('2 / 50');
    expect(screen.getByRole('button', { name: 'Edit Focus' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Delete Focus' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Add label' })).toBeTruthy();
  });

  it('shows a viewer the list and the read-only notice only', async () => {
    mocks.role = 'viewer';
    renderTab();
    expect(await screen.findByText('Focus')).toBeTruthy();
    expect(screen.getByText('Owners and managers can add and edit labels.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Add label' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Edit Focus' })).toBeNull();
  });

  it('renders the empty state', async () => {
    mocks.labels = [];
    renderTab();
    expect(await screen.findByText('No labels yet')).toBeTruthy();
  });

  it('offers a retry when the list fails to load', async () => {
    mocks.listError = true;
    renderTab();
    expect(await screen.findByText('Labels could not be loaded.')).toBeTruthy();
    mocks.listError = false;
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('Client visit')).toBeTruthy();
  });

  it('disables add at 50 labels and still renders 51', async () => {
    mocks.labels = Array.from({ length: 51 }, (_, i) => ({
      id: `l${i}`,
      name: `Label ${i}`,
      color: '#8F8F8F',
    }));
    renderTab();
    await screen.findByText('Label 0');
    expect(screen.getByTestId('label-count').textContent).toBe('51 / 50');
    expect((screen.getByRole('button', { name: 'Add label' }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    expect(screen.getByText('A calendar can have up to 50 labels.')).toBeTruthy();
  });
});

describe('LabelsTab form', () => {
  it('defaults to the first unused standard colour and previews it', async () => {
    renderTab();
    await userEvent.click(await screen.findByRole('button', { name: 'Add label' }));
    // #E73B3B and #2ECC87 are used; first free standard is #FF7043.
    expect(screen.getByRole('radio', { name: 'Color #FF7043' }).getAttribute('aria-checked')).toBe(
      'true',
    );
    expect(screen.getByTestId('event-pill-preview').style.getPropertyValue('--evt')).toBe(
      '#FF7043',
    );
  });

  it('creates a label and invalidates the label list and the event caches', async () => {
    const { invalidate } = renderTab();
    await userEvent.click(await screen.findByRole('button', { name: 'Add label' }));
    await userEvent.type(screen.getByRole('textbox', { name: 'Name' }), '  Travel ');
    await userEvent.click(screen.getByRole('radio', { name: 'Color #47B2F7' }));
    expect(screen.getByTestId('event-pill-preview').style.getPropertyValue('--evt')).toBe(
      '#47B2F7',
    );
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(mocks.calls).toHaveLength(1));
    expect(mocks.calls[0]).toEqual({ method: 'POST', body: { name: 'Travel', color: '#47B2F7' } });
    await waitFor(() => {
      const keys = invalidatedKeys(invalidate);
      expect(keys).toContainEqual(['calendars', 'labels', 'ws', 'cal']);
      expect(keys).toContainEqual(['calendar-events']);
      expect(keys).toContainEqual(['calendar', 'me-events']);
      expect(keys).toContainEqual(['events', 'detail']);
    });
  });

  it('moves the selection with the arrow keys', async () => {
    renderTab();
    await userEvent.click(await screen.findByRole('button', { name: 'Add label' }));
    const current = screen.getByRole('radio', { name: 'Color #FF7043' });
    current.focus();
    await userEvent.keyboard('{ArrowRight}');
    expect(screen.getByRole('radio', { name: 'Color #F5A623' }).getAttribute('aria-checked')).toBe(
      'true',
    );
    expect(screen.getByRole('radio', { name: 'Color #FF7043' }).getAttribute('tabindex')).toBe(
      '-1',
    );
  });

  it('blocks saving on an invalid hex and keeps an off-palette colour editable', async () => {
    mocks.labels = [{ id: 'l1', name: 'Legacy', color: '#123456' }];
    renderTab();
    await userEvent.click(await screen.findByRole('button', { name: 'Edit Legacy' }));
    const hex = screen.getByRole('textbox', { name: 'Custom (#RRGGBB)' }) as HTMLInputElement;
    expect(hex.value).toBe('#123456');
    await userEvent.clear(hex);
    await userEvent.type(hex, '#12');
    expect(screen.getByText('Enter a color as #RRGGBB.')).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('sends only the changed fields on edit', async () => {
    renderTab();
    await userEvent.click(await screen.findByRole('button', { name: 'Edit Focus' }));
    const name = screen.getByRole('textbox', { name: 'Name' });
    await userEvent.clear(name);
    await userEvent.type(name, 'Deep work');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(mocks.calls).toHaveLength(1));
    expect(mocks.calls[0]).toEqual({ method: 'PATCH', labelId: 'l2', body: { name: 'Deep work' } });
  });

  it('shows NAME_TAKEN on the name field instead of a toast', async () => {
    mocks.writeError = new ApiError(CalendarErrors.CALENDAR_LABEL_NAME_TAKEN.code, 'taken', 409);
    renderTab();
    await userEvent.click(await screen.findByRole('button', { name: 'Add label' }));
    await userEvent.type(screen.getByRole('textbox', { name: 'Name' }), 'Focus');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(
      await screen.findByText('Another label on this calendar already has this name.'),
    ).toBeTruthy();
    expect(mocks.toastShow).not.toHaveBeenCalled();
  });

  it('toasts other create failures', async () => {
    mocks.writeError = new ApiError('X.Y', 'nope', 500);
    renderTab();
    await userEvent.click(await screen.findByRole('button', { name: 'Add label' }));
    await userEvent.type(screen.getByRole('textbox', { name: 'Name' }), 'Travel');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(mocks.toastShow).toHaveBeenCalledWith({
        tone: 'danger',
        message: 'The label could not be created.',
      }),
    );
  });
});

describe('LabelsTab delete', () => {
  it('deletes after confirmation and invalidates the caches', async () => {
    mocks.confirm.mockResolvedValue(true);
    const { invalidate } = renderTab();
    await userEvent.click(await screen.findByRole('button', { name: 'Delete Focus' }));
    await waitFor(() => expect(mocks.calls).toEqual([{ method: 'DELETE', labelId: 'l2' }]));
    expect(mocks.confirm).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Delete the label "Focus"?' }),
    );
    await waitFor(() =>
      expect(invalidatedKeys(invalidate)).toContainEqual(['calendars', 'labels', 'ws', 'cal']),
    );
  });

  it('does nothing when the confirmation is declined', async () => {
    mocks.confirm.mockResolvedValue(false);
    renderTab();
    await userEvent.click(await screen.findByRole('button', { name: 'Delete Focus' }));
    await waitFor(() => expect(mocks.confirm).toHaveBeenCalled());
    expect(mocks.calls).toHaveLength(0);
  });
});
