/**
 * The theme context sits at the root of both apps, so every consumer of
 * `useThemeContext` re-renders whenever the context value's identity
 * changes. Building that object inline made the identity change on every
 * ancestor render — a parent re-rendering for an unrelated reason pushed a
 * new theme value through the whole tree.
 *
 * These tests count consumer renders rather than inspecting the object,
 * because "the value is memoized" only matters insofar as consumers stop
 * re-rendering, and a memo with the wrong dependency list would still
 * satisfy an identity check taken at one instant.
 */

import { act, cleanup, render, screen } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { useState } from 'react';
import { beforeEach, describe, expect, it } from 'vitest';

import { ThemeProvider, useThemeContext } from '../theme-provider';

let consumerRenders = 0;
let contextValues: unknown[] = [];

function Consumer(): ReactElement {
  const ctx = useThemeContext();
  consumerRenders += 1;
  contextValues.push(ctx);
  return <span data-testid="family">{ctx.family}</span>;
}

/** Re-renders its children on demand without touching the theme. */
let bumpParent: () => void = () => undefined;
function Parent({ children }: { children: ReactNode }): ReactElement {
  const [tick, setTick] = useState(0);
  bumpParent = () => {
    setTick((t) => t + 1);
  };
  return (
    <div data-tick={tick}>
      <ThemeProvider>{children}</ThemeProvider>
    </div>
  );
}

beforeEach(() => {
  cleanup();
  consumerRenders = 0;
  contextValues = [];
  localStorage.clear();
});

describe('ThemeProvider context identity', () => {
  it('does not re-render consumers when an ancestor re-renders', () => {
    render(
      <Parent>
        <Consumer />
      </Parent>,
    );
    const before = consumerRenders;
    expect(before).toBeGreaterThan(0);

    act(() => {
      bumpParent();
    });
    act(() => {
      bumpParent();
    });

    expect(consumerRenders).toBe(before);
  });

  it('keeps the same context object across unrelated ancestor renders', () => {
    render(
      <Parent>
        <Consumer />
      </Parent>,
    );
    const first = contextValues.at(-1);

    act(() => {
      bumpParent();
    });

    expect(contextValues.at(-1)).toBe(first);
  });

  it('still publishes a new value when the theme actually changes', () => {
    function Changer(): ReactElement {
      const { setFamily } = useThemeContext();
      return (
        <button
          type="button"
          onClick={() => {
            setFamily('glass');
          }}
        >
          glass
        </button>
      );
    }
    render(
      <Parent>
        <Consumer />
        <Changer />
      </Parent>,
    );
    const before = contextValues.at(-1);

    act(() => {
      screen.getByRole('button', { name: 'glass' }).click();
    });

    expect(contextValues.at(-1)).not.toBe(before);
    expect(screen.getByTestId('family').textContent).toBe('glass');
  });
});
