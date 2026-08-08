/**
 * A proposed step's priority is shown the way every other priority in
 * the product is shown, and is sent on untouched.
 *
 * propose-steps used to answer with a label ("low" / "medium" / "high")
 * while apply-steps wanted the 0..4 integer, so the panel built its own
 * label→number table and quietly sent 2 for anything it had no entry
 * for. Both sides now speak the 0..4 scale, which leaves two ways to get
 * this wrong: render the raw number (a badge reading "3"), or build a
 * second set of labels beside the ones the task list already uses. These
 * tests fail on either.
 *
 * The test i18n instance echoes missing keys back, so the badge text is
 * the key itself — which is exactly what is being asserted about.
 */

import { fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { TaskPriority } from '../api';
import { PRIORITY_KEY } from '../constants';

const applyMutate = vi.fn().mockResolvedValue({ created: [] });
const proposedPriorities: { current: TaskPriority[] } = { current: [2] };

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

vi.mock('../steps-api', async () => {
  const actual = await vi.importActual<typeof import('../steps-api')>('../steps-api');
  return {
    ...actual,
    useProposeSteps: () => ({
      mutateAsync: vi.fn().mockImplementation(() =>
        Promise.resolve({
          parentTaskId: 'task-001',
          steps: proposedPriorities.current.map((priority, i) => ({
            title: `Step ${String(i)}`,
            description: '',
            priority,
            uiId: `ui-step-${String(i)}`,
          })),
        }),
      ),
      isPending: false,
    }),
    useApplySteps: () => ({
      mutateAsync: applyMutate,
      isPending: false,
    }),
  };
});

import TaskStepsPanel from '../task-steps-panel';

/** Renders the panel and runs the propose action. */
async function proposeWith(priorities: TaskPriority[]): Promise<void> {
  proposedPriorities.current = priorities;
  renderWithProviders(<TaskStepsPanel taskId="task-001" workspaceId="ws-001" />);
  fireEvent.click(screen.getByRole('button', { name: /tasks\.steps\.propose_button/i }));
  await waitFor(() => {
    expect(screen.getByDisplayValue('Step 0')).toBeDefined();
  });
}

afterEach(() => {
  applyMutate.mockClear();
});

describe('<TaskStepsPanel> priority', () => {
  it('labels each level through the shared task priority keys', async () => {
    const levels: TaskPriority[] = [0, 1, 2, 3, 4];
    await proposeWith(levels);

    for (const level of levels) {
      const key = PRIORITY_KEY[level];
      expect(screen.getAllByText(key).length, `no badge rendered ${key}`).toBeGreaterThan(0);
    }
  });

  it('never puts the raw number on screen', async () => {
    await proposeWith([3]);

    // A badge reading "3", or a key with the number spliced into it, is
    // what an unmapped integer looks like to a reader.
    expect(screen.queryByText('3')).toBeNull();
    expect(screen.queryByText(/priority_3/)).toBeNull();
    expect(screen.getAllByText('tasks.priority.high').length).toBeGreaterThan(0);
  });

  it('sends the proposed priority on unchanged', async () => {
    await proposeWith([0, 4]);

    fireEvent.click(screen.getByRole('button', { name: /tasks\.steps\.apply_button/i }));

    await waitFor(() => {
      expect(applyMutate).toHaveBeenCalledTimes(1);
    });
    const args = applyMutate.mock.calls[0]?.[0] as {
      steps: { priority: number }[];
    };
    // Both ends of the scale survive. A client-side remap would land
    // them somewhere in the middle, which is the failure the old
    // `?? 2` default produced without saying anything.
    expect(args.steps.map((s) => s.priority)).toEqual([0, 4]);
  });
});
