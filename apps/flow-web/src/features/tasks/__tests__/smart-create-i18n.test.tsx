/**
 * Smart-create subtask proposal rows must route the AI-supplied priority
 * label through i18n rather than printing the raw provider string
 * directly. The provider returns priority as one of
 * 'low' | 'medium' | 'high' | 'urgent' (plus, defensively, anything
 * else it might one day emit); rendering that value verbatim would
 * leak an untranslated English word into ja/zh UIs.
 *
 * This drives the real dialog through its AI Assist flow (fill title,
 * click "assist", resolve a mocked proposal) and asserts the rendered
 * badge text is the translation *key* the passthrough test-i18n
 * returns — never the raw priority string.
 */

import { fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';

import type { SmartProposal } from '../smart-create-api';
import TaskCreateDialog from '../task-create-dialog';

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    useCreateTask: () => ({ mutateAsync: vi.fn(), isPending: false }),
  };
});

const proposal: SmartProposal = {
  suggestedAssignees: [],
  subtasks: [
    { title: 'Low task', description: '', priority: 'low' },
    { title: 'Medium task', description: '', priority: 'medium' },
    { title: 'High task', description: '', priority: 'high' },
    { title: 'Urgent task', description: '', priority: 'urgent' },
    // A value outside the known set — the row must still route it
    // through the interpolated "unknown" key rather than printing it
    // bare.
    { title: 'Odd task', description: '', priority: 'critical' },
  ],
};

vi.mock('../smart-create-api', async () => {
  const actual = await vi.importActual<typeof import('../smart-create-api')>('../smart-create-api');
  return {
    ...actual,
    useProposeSmartTask: () => ({
      mutate: (_args: unknown, opts: { onSuccess: (data: SmartProposal) => void }) => {
        opts.onSuccess(proposal);
      },
      isPending: false,
    }),
    useApplySmartTask: () => ({ mutateAsync: vi.fn(), isPending: false }),
  };
});

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

// The dialog's DatePicker calls t('common.date.weekdays', { returnObjects: true }),
// which the shared passthrough test-i18n cannot satisfy (it always returns a
// string). Stub react-i18next narrowly so every other call stays passthrough.
vi.mock('react-i18next', async () => {
  const actual = await vi.importActual<typeof import('react-i18next')>('react-i18next');
  return {
    ...actual,
    useTranslation: () => ({
      t: (key: string, options?: { returnObjects?: boolean }) => {
        if (options?.returnObjects && key === 'common.date.weekdays') {
          return ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
        }
        return key;
      },
      i18n: { resolvedLanguage: 'en' },
    }),
  };
});

describe('smart-create subtask priority labels', () => {
  it('routes each known priority through its dedicated t() key, not the raw value', async () => {
    renderWithProviders(
      <TaskCreateDialog projectId="proj-001" workspaceId="ws-001" open={true} onClose={vi.fn()} />,
    );

    const titleInput = screen.getByRole('textbox', { name: /tasks\.form\.title/i });
    fireEvent.change(titleInput, { target: { value: 'Ship the release' } });

    fireEvent.click(screen.getByRole('button', { name: 'tasks.smart_create.assist_button' }));

    await waitFor(() => {
      expect(screen.queryByText('Low task')).not.toBeNull();
    });

    // Every known priority must render as its dedicated translation key,
    // never the bare word the AI provider returned.
    expect(screen.getByText('tasks.steps.priority_low')).not.toBeNull();
    expect(screen.getByText('tasks.steps.priority_medium')).not.toBeNull();
    expect(screen.getByText('tasks.steps.priority_high')).not.toBeNull();
    expect(screen.getByText('tasks.smart_create.priority_urgent')).not.toBeNull();

    expect(screen.queryByText('low')).toBeNull();
    expect(screen.queryByText('medium')).toBeNull();
    expect(screen.queryByText('high')).toBeNull();
    expect(screen.queryByText('urgent')).toBeNull();

    // An unrecognised priority value falls back to the interpolated
    // "unknown" key rather than being printed bare.
    expect(screen.getByText('tasks.smart_create.priority_unknown')).not.toBeNull();
    expect(screen.queryByText('critical')).toBeNull();
  });
});
