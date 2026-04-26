/**
 * @brief Vitest coverage for the `useZodForm` hook.
 *
 * The tests assert three contracts:
 *   1. zod validation errors flow through `formState.errors`.
 *   2. submission of valid input invokes the handler with parsed values.
 *   3. `setApiErrors` projects server-side messages onto field errors via
 *      `react-hook-form`'s standard `setError` machinery.
 *
 * `react-hook-form` exposes `formState` through a Proxy that only schedules
 * re-renders for the keys it observes accessed during a render pass. When
 * exercising the hook through `renderHook` we therefore read `errors` (and
 * `isSubmitting`) inside the hook callback itself, otherwise RHF never
 * re-renders after `setError` / validation completes and the assertions
 * read stale snapshots.
 */

import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { z } from 'zod';
import { useZodForm } from './use-zod-form';

const schema = z.object({
  displayName: z.string().min(1, 'displayName.required'),
  email: z.string().email('email.invalid'),
});

function renderUseZodForm(defaults: { displayName: string; email: string }) {
  return renderHook(() => {
    const form = useZodForm(schema, defaults);
    // Touch the proxy keys we assert on so RHF subscribes.
    void form.formState.errors;
    void form.formState.isSubmitting;
    return form;
  });
}

describe('useZodForm', () => {
  it('exposes zod validation errors through react-hook-form', async () => {
    const { result } = renderUseZodForm({ displayName: '', email: 'not-an-email' });

    await act(async () => {
      await result.current.handleSubmit(() => undefined)();
    });

    await waitFor(() => {
      expect(result.current.formState.errors.displayName?.message).toBe('displayName.required');
    });
    expect(result.current.formState.errors.email?.message).toBe('email.invalid');
  });

  it('invokes the submit handler with parsed values when input is valid', async () => {
    const onSubmit = vi.fn();
    const { result } = renderUseZodForm({ displayName: 'Ada', email: 'ada@example.test' });

    await act(async () => {
      await result.current.handleSubmit(onSubmit)();
    });

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0]?.[0]).toEqual({
      displayName: 'Ada',
      email: 'ada@example.test',
    });
    expect(result.current.formState.errors.displayName).toBeUndefined();
    expect(result.current.formState.errors.email).toBeUndefined();
  });

  it('projects server-side errors onto fields via setApiErrors', async () => {
    const { result } = renderUseZodForm({ displayName: 'Ada', email: 'ada@example.test' });

    act(() => {
      result.current.setApiErrors([
        { field: 'displayName', message: 'displayName.taken' },
        { field: 'email', message: 'email.in_use', type: 'conflict' },
      ]);
    });

    await waitFor(() => {
      expect(result.current.formState.errors.displayName?.message).toBe('displayName.taken');
    });
    expect(result.current.formState.errors.email?.message).toBe('email.in_use');
    expect(result.current.formState.errors.email?.type).toBe('conflict');
  });
});
