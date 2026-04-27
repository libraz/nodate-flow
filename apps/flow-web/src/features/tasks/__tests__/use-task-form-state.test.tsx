import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { useTaskFormState } from '../use-task-form-state';

describe('useTaskFormState', () => {
  it('starts with empty title, description, and no error', () => {
    const { result } = renderHook(() => useTaskFormState());
    expect(result.current.title).toBe('');
    expect(result.current.description).toBe('');
    expect(result.current.titleError).toBeNull();
  });

  it('updates fields independently', () => {
    const { result } = renderHook(() => useTaskFormState());
    act(() => {
      result.current.setTitle('Hello');
      result.current.setDescription('World');
      result.current.setTitleError('required');
    });
    expect(result.current.title).toBe('Hello');
    expect(result.current.description).toBe('World');
    expect(result.current.titleError).toBe('required');
  });

  it('reset() clears every field', () => {
    const { result } = renderHook(() => useTaskFormState());
    act(() => {
      result.current.setTitle('Hello');
      result.current.setDescription('World');
      result.current.setTitleError('required');
    });
    act(() => {
      result.current.reset();
    });
    expect(result.current.title).toBe('');
    expect(result.current.description).toBe('');
    expect(result.current.titleError).toBeNull();
  });
});
