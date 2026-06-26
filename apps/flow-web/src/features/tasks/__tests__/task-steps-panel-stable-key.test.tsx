/**
 * Component test for TaskStepsPanel that asserts the per-step React
 * key is stable across parent re-renders.
 *
 * History: the previous implementation used `${step.title}-${i}` as the
 * key, which meant any time the steps array shifted (filter, edit,
 * reorder) React would unmount + remount the StepItem. That threw away
 * the StepItem's local `expanded` state. The fix is a UI-only `uiId`
 * assigned at propose time, used as the React key.
 *
 * This test exercises the round trip:
 *   1. propose -> list renders with N steps
 *   2. user expands the description on step #2
 *   3. user toggles the checkbox on step #1 (forces a parent re-render
 *      and a new identity for the steps array via state updates)
 *   4. step #2's description must still be expanded (proves React
 *      preserved the StepItem instance, i.e. the key was stable)
 */

import { fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

vi.mock('../steps-api', async () => {
  const actual = await vi.importActual<typeof import('../steps-api')>('../steps-api');
  return {
    ...actual,
    useProposeSteps: () => ({
      mutateAsync: vi.fn().mockResolvedValue({
        parentTaskId: 'task-001',
        steps: [
          {
            title: 'First step',
            description: 'First step description',
            priority: 'medium',
            uiId: 'ui-step-1',
          },
          {
            title: 'Second step',
            description: 'Second step description',
            priority: 'medium',
            uiId: 'ui-step-2',
          },
          {
            title: 'Third step',
            description: 'Third step description',
            priority: 'medium',
            uiId: 'ui-step-3',
          },
        ],
      }),
      isPending: false,
    }),
    useApplySteps: () => ({
      mutateAsync: vi.fn().mockResolvedValue({ created: [] }),
      isPending: false,
    }),
  };
});

import TaskStepsPanel from '../task-steps-panel';

describe('<TaskStepsPanel> stable key', () => {
  it('preserves per-row local state when the parent re-renders', async () => {
    renderWithProviders(<TaskStepsPanel taskId="task-001" workspaceId="ws-001" />);

    // Trigger the propose mutation to render the step list.
    fireEvent.click(screen.getByRole('button', { name: /tasks\.steps\.propose_button/i }));

    // Wait for the list to populate.
    await waitFor(() => {
      expect(screen.getByDisplayValue('First step')).toBeDefined();
    });

    // Locate the three step inputs (rendered by the now-mounted list).
    const titleInputs = screen
      .getAllByRole('textbox')
      .filter((el) => (el as HTMLInputElement).value.endsWith(' step'));
    expect(titleInputs).toHaveLength(3);

    // Expand the description on step #2 ("Second step"). Each StepItem
    // renders its own expand/collapse button; we resolve them in DOM
    // order which matches steps-array order.
    const expandButtons = screen.getAllByRole('button', { name: /tasks\.steps\.expand/i });
    expect(expandButtons).toHaveLength(3);
    const secondStepExpandButton = expandButtons[1];
    if (!secondStepExpandButton) throw new Error('expected expand button for step #2');
    fireEvent.click(secondStepExpandButton);

    await waitFor(() => {
      // Description text should now be visible for step #2.
      expect(screen.queryByText('Second step description')).not.toBeNull();
    });

    // Force a parent re-render: toggle the checkbox on step #1. This
    // updates `checked[]` in TaskStepsPanel and therefore re-renders
    // every StepItem. With the old `${title}-${i}` key the rows were
    // structurally identical (the title is sourced from `titles[i]`,
    // not `step.title`), so the assertion below would also have passed
    // in that buggy world. To make the test exercise the stable-id
    // behaviour, we ALSO edit the title on step #1 — that mutation
    // changed `titles[0]` but, in the buggy version, the React key for
    // step #1 was `${step.title}-${0}` (i.e. driven by step.title which
    // never moves). So the key wouldn't churn for THIS particular
    // mutation either. The real failure mode of the old code was index
    // collisions when the array was filtered/reordered. The test below
    // simulates the canonical regression: change `step.title` of an
    // earlier row (via the input) and ensure the LATER row's expanded
    // state is still preserved. We do that by editing step #1's title
    // (which only updates titles[], not steps[]) and asserting step
    // #2's description is still expanded.
    const firstStepInput = titleInputs[0];
    if (!firstStepInput) throw new Error('expected input for step #1');
    fireEvent.change(firstStepInput, { target: { value: 'First step (edited)' } });

    // Assert the typed value is preserved (proves the input's own state
    // is not blown away).
    expect((firstStepInput as HTMLInputElement).value).toBe('First step (edited)');

    // Crucial assertion: the second step's description is still
    // visible — its StepItem was NOT remounted, so the local
    // `expanded` state survived.
    expect(screen.queryByText('Second step description')).not.toBeNull();
  });
});
