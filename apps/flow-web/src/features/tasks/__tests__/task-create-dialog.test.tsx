/**
 * Component tests for TaskCreateDialog.
 *
 * Covers form-reset timing on submit failure: when the create mutation
 * rejects, the dialog must keep field values populated so the user can
 * retry without re-entering data.
 */

import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from '../../../test/helpers/render';
import TaskCreateDialog from '../task-create-dialog';

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    useCreateTask: () => ({
      mutateAsync: vi.fn().mockRejectedValue(new Error('boom')),
      isPending: false,
    }),
  };
});

vi.mock('../smart-create-api', () => ({
  useProposeSmartTask: () => ({ mutate: vi.fn(), isPending: false }),
  useApplySmartTask: () => ({
    mutateAsync: vi.fn().mockRejectedValue(new Error('boom')),
    isPending: false,
  }),
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

// Override react-i18next so the dialog's `t('common.date.weekdays', { returnObjects: true })`
// call resolves to an array. The shared test renderer uses passthrough i18n
// which returns the key string and crashes DatePicker.
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

describe('<TaskCreateDialog>', () => {
  it('keeps the title field populated when create mutation fails', async () => {
    renderWithProviders(<TaskCreateDialog projectId="proj-001" open={true} onClose={vi.fn()} />);

    const titleInput = screen.getByRole('textbox', { name: /tasks\.form\.title/i });
    fireEvent.change(titleInput, { target: { value: 'Draft a release plan' } });
    expect((titleInput as HTMLInputElement).value).toBe('Draft a release plan');

    const submitButton = screen.getByRole('button', { name: /tasks\.form\.submit/i });
    fireEvent.submit(submitButton.closest('form') as HTMLFormElement);

    await waitFor(() => {
      // The mutation rejects; the form must remain populated.
      expect((titleInput as HTMLInputElement).value).toBe('Draft a release plan');
    });
  });

  it('clears the title field error when the user starts typing again', async () => {
    renderWithProviders(<TaskCreateDialog projectId="proj-001" open={true} onClose={vi.fn()} />);

    const titleInput = screen.getByRole('textbox', { name: /tasks\.form\.title/i });
    const submitButton = screen.getByRole('button', { name: /tasks\.form\.submit/i });

    // Submit empty: zod schema should produce a title_required error.
    fireEvent.submit(submitButton.closest('form') as HTMLFormElement);

    await waitFor(() => {
      expect(screen.queryByText(/tasks\.validation\.title_required/i)).not.toBeNull();
    });

    fireEvent.change(titleInput, { target: { value: 'something' } });

    await waitFor(() => {
      expect(screen.queryByText(/tasks\.validation\.title_required/i)).toBeNull();
    });
  });
});
