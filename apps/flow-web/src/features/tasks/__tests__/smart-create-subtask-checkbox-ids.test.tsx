/**
 * Smart-create subtask checkbox ids must be independent of
 * the AI-provided proposal title. Two subtask proposals can carry the
 * identical title (the model is not guaranteed to de-duplicate its own
 * suggestions), and an id derived from the title collides in that
 * case. Duplicate DOM ids break `<label for>` association: clicking
 * the second row's label activates whichever checkbox the browser
 * finds first via `getElementById`, which is the wrong row.
 *
 * This renders a proposal with two subtasks sharing the exact same
 * title and asserts every rendered checkbox id is unique.
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

const duplicateTitleProposal: SmartProposal = {
  suggestedAssignees: [],
  subtasks: [
    { title: 'Write tests', description: 'first', priority: 'low' },
    { title: 'Write tests', description: 'second', priority: 'high' },
  ],
};

vi.mock('../smart-create-api', async () => {
  const actual = await vi.importActual<typeof import('../smart-create-api')>('../smart-create-api');
  return {
    ...actual,
    useProposeSmartTask: () => ({
      mutate: (_args: unknown, opts: { onSuccess: (data: SmartProposal) => void }) => {
        opts.onSuccess(duplicateTitleProposal);
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

describe('smart-create subtask checkbox ids', () => {
  it('assigns each duplicate-titled subtask a distinct checkbox id', async () => {
    renderWithProviders(
      <TaskCreateDialog projectId="proj-001" workspaceId="ws-001" open={true} onClose={vi.fn()} />,
    );

    const titleInput = screen.getByRole('textbox', { name: /tasks\.form\.title/i });
    fireEvent.change(titleInput, { target: { value: 'Ship the release' } });
    fireEvent.click(screen.getByRole('button', { name: 'tasks.smart_create.assist_button' }));

    await waitFor(() => {
      expect(screen.getAllByText('Write tests')).toHaveLength(2);
    });

    // The proposal has no suggested assignees, so every rendered
    // checkbox belongs to a subtask row.
    const checkboxes = screen.getAllByRole('checkbox') as HTMLInputElement[];

    // Non-empty control: the row-level checkboxes actually rendered.
    expect(checkboxes.length).toBe(2);
    expect(checkboxes[0]?.checked).toBe(true);
    expect(checkboxes[1]?.checked).toBe(true);

    const ids = checkboxes.map((cb) => cb.id);
    expect(new Set(ids).size).toBe(ids.length);

    // Duplicate ids would misroute a label click to the wrong row's
    // checkbox (the browser resolves `for` via the first matching id).
    // Toggling the second row's label must only affect the second
    // checkbox.
    const secondLabel = checkboxes[1]
      ? document.querySelector(`label[for="${checkboxes[1].id}"]`)
      : null;
    expect(secondLabel).not.toBeNull();
    fireEvent.click(secondLabel as Element);

    expect(checkboxes[0]?.checked).toBe(true);
    expect(checkboxes[1]?.checked).toBe(false);
  });
});
