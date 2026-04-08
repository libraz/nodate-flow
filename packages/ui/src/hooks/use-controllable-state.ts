/**
 * useControllableState — supports both controlled (value + onChange) and
 * uncontrolled (defaultValue) usage patterns from a single hook.
 */

import { useCallback, useRef, useState } from 'react';

export interface UseControllableStateOptions<T> {
  value?: T | undefined;
  defaultValue?: T | undefined;
  onChange?: ((next: T) => void) | undefined;
}

export function useControllableState<T>(
  options: UseControllableStateOptions<T>,
): [T | undefined, (next: T) => void] {
  const { value, defaultValue, onChange } = options;
  const isControlled = value !== undefined;

  const [internal, setInternal] = useState<T | undefined>(defaultValue);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  const current = isControlled ? value : internal;

  const setValue = useCallback(
    (next: T) => {
      if (!isControlled) setInternal(next);
      onChangeRef.current?.(next);
    },
    [isControlled],
  );

  return [current, setValue];
}
