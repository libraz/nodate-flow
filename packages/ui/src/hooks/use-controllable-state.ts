/**
 * useControllableState — supports both controlled (value + onChange) and
 * uncontrolled (defaultValue) usage patterns from a single hook.
 *
 * Which mode a component is in is decided once, on the first render, and
 * never again. It used to be recomputed from `value !== undefined` on
 * every render, which meant a controlled component whose value was
 * momentarily undefined became uncontrolled for that render: writes went
 * to internal state the parent could not see, and a later render that
 * passed undefined again resurfaced that stale internal value as if the
 * parent had asked for it. Nothing reported any of it.
 *
 * Latching matches how React treats its own inputs, and like React we
 * complain in development when the two disagree afterwards — a component
 * that starts undefined and becomes controlled is a real mistake, and it
 * should be loud rather than silently half-working.
 */

import { useCallback, useRef, useState } from 'react';

export interface UseControllableStateOptions<T> {
  value?: T | undefined;
  defaultValue?: T | undefined;
  onChange?: ((next: T) => void) | undefined;
  /** Component name used in the development warning. */
  name?: string | undefined;
}

export function useControllableState<T>(
  options: UseControllableStateOptions<T>,
): [T | undefined, (next: T) => void] {
  const { value, defaultValue, onChange, name } = options;

  const isControlledRef = useRef(value !== undefined);
  const isControlled = isControlledRef.current;

  const [internal, setInternal] = useState<T | undefined>(defaultValue);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  const warnedRef = useRef(false);
  if (process.env.NODE_ENV !== 'production' && !warnedRef.current) {
    const nowControlled = value !== undefined;
    if (nowControlled !== isControlled) {
      warnedRef.current = true;
      const label = name ?? 'A component';
      console.error(
        isControlled
          ? `${label} is changing from controlled to uncontrolled. Pass the controlled value for the whole lifetime of the component, using null or an empty value rather than undefined for "no selection".`
          : `${label} is changing from uncontrolled to controlled. Decide before the first render: pass \`value\` for the whole lifetime, or pass \`defaultValue\` and let the component own the state.`,
      );
    }
  }

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
