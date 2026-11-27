/**
 * Creating a label from the picker's search box, and applying it to the task.
 */

import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';

import type { Label } from '../api';
import { pickLabelColor } from '../label-color';
import LabelPicker from '../label-picker';
import TaskLabelsField from '../task-labels-field';

const mocks = vi.hoisted(() => ({
  createAsync: vi.fn(),
  addAsync: vi.fn(),
  add: vi.fn(),
  remove: vi.fn(),
}));

vi.mock('../api', () => ({
  useLabelsQuery: () => ({ data: [{ id: 'l1', name: 'Bug', color: '#E73B3B' }] }),
  useTaskLabelsQuery: () => ({ data: [] }),
  useCreateLabel: () => ({ mutateAsync: mocks.createAsync, isPending: false }),
  useAddTaskLabel: () => ({ mutateAsync: mocks.addAsync, mutate: mocks.add }),
  useRemoveTaskLabel: () => ({ mutate: mocks.remove }),
}));

const LABELS: Label[] = [{ id: 'l1', name: 'Bug', color: '#E73B3B' }];

describe('LabelPicker create option', () => {
  it('offers create for an unmatched name and picks it with Enter', async () => {
    const user = userEvent.setup();
    const onCreate = vi.fn();
    renderWithProviders(
      <LabelPicker labels={LABELS} activeIds={[]} onToggle={vi.fn()} onCreate={onCreate} />,
    );
    await user.click(screen.getByRole('button'));
    await user.type(screen.getByRole('combobox'), 'Docs');
    expect(screen.getByRole('option', { name: /picker\.create_new/ })).toBeTruthy();
    await user.keyboard('{Enter}');
    expect(onCreate).toHaveBeenCalledWith('Docs');
  });

  it('hides create on an exact (case-insensitive) match and toggles on Enter', async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    const onCreate = vi.fn();
    renderWithProviders(
      <LabelPicker labels={LABELS} activeIds={[]} onToggle={onToggle} onCreate={onCreate} />,
    );
    await user.click(screen.getByRole('button'));
    await user.type(screen.getByRole('combobox'), 'bug');
    expect(screen.queryByText(/picker\.create_new/)).toBeNull();
    await user.keyboard('{Enter}');
    expect(onToggle).toHaveBeenCalledWith('l1', true);
    expect(onCreate).not.toHaveBeenCalled();
  });

  it('moves the highlighted row with the arrow keys', async () => {
    const user = userEvent.setup();
    const onCreate = vi.fn();
    renderWithProviders(
      <LabelPicker labels={LABELS} activeIds={[]} onToggle={vi.fn()} onCreate={onCreate} />,
    );
    await user.click(screen.getByRole('button'));
    await user.type(screen.getByRole('combobox'), 'Bu');
    await user.keyboard('{ArrowDown}{Enter}');
    expect(onCreate).toHaveBeenCalledWith('Bu');
  });

  it('says there are no labels in an empty workspace', async () => {
    const user = userEvent.setup();
    renderWithProviders(<LabelPicker labels={[]} activeIds={[]} onToggle={vi.fn()} />);
    await user.click(screen.getByRole('button'));
    expect(screen.getByText('picker.no_labels')).toBeTruthy();
  });
});

describe('TaskLabelsField', () => {
  it('creates the label and then applies it to the task', async () => {
    const user = userEvent.setup();
    mocks.createAsync.mockResolvedValue({ id: 'new-id', name: 'Docs', color: '#fff' });
    mocks.addAsync.mockResolvedValue(undefined);
    renderWithProviders(<TaskLabelsField taskId="t1" workspaceId="ws1" />);
    await user.click(screen.getByRole('button', { name: 'task.add' }));
    await user.type(screen.getByRole('combobox'), 'Docs{Enter}');
    await waitFor(() =>
      expect(mocks.addAsync).toHaveBeenCalledWith({ taskId: 't1', labelId: 'new-id' }),
    );
    expect(mocks.createAsync).toHaveBeenCalledWith({
      workspaceId: 'ws1',
      input: { name: 'Docs', color: pickLabelColor('Docs') },
    });
  });

  it('picks the same colour for the same name', () => {
    expect(pickLabelColor('Docs')).toBe(pickLabelColor('Docs'));
    expect(pickLabelColor('Docs')).toMatch(/^#[0-9A-Fa-f]{6}$/);
  });
});
