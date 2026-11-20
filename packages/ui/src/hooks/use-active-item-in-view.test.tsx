import { render } from '@testing-library/react';
import { type ReactElement, useRef } from 'react';
import { describe, expect, it } from 'vitest';

import { useActiveItemInView } from './use-active-item-in-view';

/** Lays the list out as a 100px viewport over 300px of items, 100px each. */
function layout(list: HTMLElement, items: Map<string, HTMLElement>): void {
  Object.defineProperty(list, 'clientWidth', { configurable: true, value: 100 });
  Object.defineProperty(list, 'scrollWidth', { configurable: true, value: 300 });
  list.getBoundingClientRect = () => new DOMRect(0, 0, 100, 20);
  let i = 0;
  for (const item of items.values()) {
    const left = i * 100;
    item.getBoundingClientRect = () => new DOMRect(left - list.scrollLeft, 0, 100, 20);
    i += 1;
  }
}

function List({ active }: { active: string }): ReactElement {
  const listRef = useRef<HTMLDivElement | null>(null);
  const items = useRef<Map<string, HTMLElement>>(new Map());
  useActiveItemInView(listRef, items, active);
  return (
    <div
      ref={(node) => {
        listRef.current = node;
        if (node) layout(node, items.current);
      }}
      data-testid="list"
    >
      {['a', 'b', 'c'].map((key) => (
        <span
          key={key}
          ref={(node) => {
            if (node) items.current.set(key, node);
          }}
        />
      ))}
    </div>
  );
}

describe('useActiveItemInView', () => {
  it('scrolls an item past the end into view, and back for one before the start', () => {
    const { getByTestId, rerender } = render(<List active="a" />);
    const list = getByTestId('list');
    expect(list.scrollLeft).toBe(0);

    rerender(<List active="c" />);
    expect(list.scrollLeft).toBe(200);

    rerender(<List active="a" />);
    expect(list.scrollLeft).toBe(0);
  });
});
