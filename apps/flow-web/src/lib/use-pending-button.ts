/**
 * usePendingButton — derive button props from a TanStack Query mutation.
 *
 * Centralizes the `disabled={mutation.isPending}` + `aria-busy` boilerplate
 * that appears at every form submit / confirm button site. Extra disabled
 * gates (form validity, custom flags, additional concurrent mutations)
 * compose via the optional `extraDisabled` array.
 *
 * @example
 * ```tsx
 * const create = useCreateThing();
 * const submit = usePendingButton(create, () => onSubmit());
 * return <Button {...submit}>{t('save')}</Button>;
 * ```
 */

import type { MouseEvent, MouseEventHandler } from 'react';

/**
 * Minimal subset of a TanStack Query mutation that we read.
 * Kept structural so any mutation result (with optional `isLoading`
 * legacy alias) can be passed without coupling to the full generic.
 */
export interface PendingMutationLike {
  /** True while the mutation is in flight (TanStack v5). */
  readonly isPending?: boolean;
  /** Legacy alias, kept for older call sites that still surface it. */
  readonly isLoading?: boolean;
}

/** Props returned by {@link usePendingButton}, spreadable on a `<button>` / `<Button>`. */
export interface PendingButtonProps {
  /** Disabled when the mutation is pending or any extra gate is true. */
  disabled: boolean;
  /** ARIA busy hint mirrors the pending state for assistive tech. */
  'aria-busy': boolean;
  /** Wrapped click handler — no-op while pending; passes through otherwise. */
  onClick: MouseEventHandler<HTMLButtonElement> | undefined;
}

/**
 * Build button props from a mutation and an optional click handler.
 *
 * @param mutation - A TanStack mutation result (or any object with
 *                   `isPending` / `isLoading`).
 * @param onClick  - The handler to invoke when the button is clicked
 *                   and the mutation is not pending. Pass `undefined`
 *                   for `<button type="submit">` forms.
 * @param extraDisabled - Additional truthy gates that should also
 *                        disable the button (e.g. form validity).
 *                        Order-independent; any truthy value disables.
 * @returns Props to spread on the button element.
 */
export function usePendingButton(
  mutation: PendingMutationLike,
  onClick?: MouseEventHandler<HTMLButtonElement>,
  extraDisabled: readonly boolean[] = [],
): PendingButtonProps {
  const pending = mutation.isPending === true || mutation.isLoading === true;
  const disabled = pending || extraDisabled.some((flag) => flag);
  const wrappedOnClick: MouseEventHandler<HTMLButtonElement> | undefined = onClick
    ? (event: MouseEvent<HTMLButtonElement>) => {
        if (pending) return;
        onClick(event);
      }
    : undefined;
  return {
    disabled,
    'aria-busy': pending,
    onClick: wrappedOnClick,
  };
}
