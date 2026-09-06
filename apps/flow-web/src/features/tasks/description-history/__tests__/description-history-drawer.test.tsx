/**
 * The description-history drawer: getting out of it, reading a mention in
 * the diff, and knowing which revision a marker stands for.
 */

import { QueryClient } from '@tanstack/react-query';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';

import type { DescriptionVersion, DescriptionVersionFull } from '../../description-history-api';
import { descriptionHistoryKeys } from '../../description-history-api';
import DescriptionHistoryDrawer from '../description-history-drawer';

const TASK_ID = 'task-1';
const VERSION_ID = 'version-1';
const ANN_ID = '019649b0-0000-7000-8000-000000000000';

const VERSION: DescriptionVersion = {
  id: VERSION_ID,
  versionNumber: 3,
  authorDisplayName: 'Ann Rivers',
  createdAt: 1_700_000_000,
  bodyLength: 12,
};

/** A client already holding the history, so nothing is fetched. */
function seededClient(body: string): QueryClient {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false, staleTime: Number.POSITIVE_INFINITY },
      mutations: { retry: false, throwOnError: false },
    },
  });
  const full: DescriptionVersionFull = { ...VERSION, body };
  client.setQueryData(descriptionHistoryKeys.all(TASK_ID), [VERSION]);
  client.setQueryData(descriptionHistoryKeys.version(TASK_ID, VERSION_ID), full);
  return client;
}

describe('the description-history drawer', () => {
  it('closes from a named control in the drawer itself', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    renderWithProviders(
      <DescriptionHistoryDrawer taskId={TASK_ID} currentBody="Ship it" open onClose={onClose} />,
      { queryClient: seededClient('Ship it') },
    );

    const close = screen.getByRole('button', { name: 'tasks.history.close' });
    await user.click(close);

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('shows a mention in the diff as the person name, never the stored notation', () => {
    renderWithProviders(
      <DescriptionHistoryDrawer
        taskId={TASK_ID}
        currentBody={`Ping @[Bee Marsh](user:${ANN_ID}) today`}
        open
        onClose={vi.fn()}
      />,
      { queryClient: seededClient(`Ping @[Ann Rivers](user:${ANN_ID}) today`) },
    );

    const chip = document.querySelector(`[data-nf-mention="${ANN_ID}"]`);
    expect(chip).toBeTruthy();
    expect(chip?.textContent).toBe('@Ann Rivers');

    const rendered = document.body.textContent ?? '';
    expect(rendered).not.toContain(ANN_ID);
    expect(rendered).not.toContain('](user:');
    expect(rendered).not.toContain('@[');
  });

  it('still reports a change when only the person a mention points at changed', () => {
    const otherId = '019649b0-0000-7000-8000-000000000001';
    renderWithProviders(
      <DescriptionHistoryDrawer
        taskId={TASK_ID}
        currentBody={`Ping @[Ann Rivers](user:${otherId})`}
        open
        onClose={vi.fn()}
      />,
      { queryClient: seededClient(`Ping @[Ann Rivers](user:${ANN_ID})`) },
    );

    expect(screen.queryByText('tasks.history.no_changes')).toBeNull();
    expect(screen.getAllByText('@Ann Rivers').length).toBe(2);
  });

  it('names the revision each marker belongs to in words', () => {
    renderWithProviders(
      <DescriptionHistoryDrawer
        taskId={TASK_ID}
        currentBody="Ship it on Friday"
        open
        onClose={vi.fn()}
      />,
      { queryClient: seededClient('Ship it') },
    );

    const legend = screen.getByRole('list', { name: 'tasks.history.diff.legend_label' });
    expect(legend.textContent).toContain('tasks.history.diff.added');
    expect(legend.textContent).toContain('tasks.history.diff.removed');
    expect(screen.getByText('tasks.history.diff.added_side')).toBeTruthy();
    expect(screen.getByText('tasks.history.diff.removed_side')).toBeTruthy();
  });
});
