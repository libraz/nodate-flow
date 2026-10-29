/**
 * usePresenceList keeps a removed item in place, marked absent, until its
 * exit is reported — and must not loop when the caller hands it a freshly
 * filtered array on every render.
 */

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { usePresenceList } from './use-presence-list';

interface Row {
  id: string;
  title: string;
}

const getKey = (row: Row): string => row.id;

function rows(...ids: string[]): Row[] {
  return ids.map((id) => ({ id, title: `title ${id}` }));
}

describe('usePresenceList', () => {
  it('keeps a removed item at its position until onExited', () => {
    const { result, rerender } = renderHook(({ items }) => usePresenceList(items, getKey), {
      initialProps: { items: rows('a', 'b', 'c') },
    });

    rerender({ items: rows('a', 'c') });
    expect(result.current.entries.map((e) => [e.key, e.present])).toEqual([
      ['a', true],
      ['b', false],
      ['c', true],
    ]);

    act(() => result.current.onExited('b'));
    expect(result.current.entries.map((e) => e.key)).toEqual(['a', 'c']);
  });

  it('brings an item back when a removal is rolled back mid-exit', () => {
    const { result, rerender } = renderHook(({ items }) => usePresenceList(items, getKey), {
      initialProps: { items: rows('a', 'b') },
    });

    rerender({ items: rows('a') });
    rerender({ items: rows('a', 'b') });
    expect(result.current.entries.map((e) => [e.key, e.present])).toEqual([
      ['a', true],
      ['b', true],
    ]);
  });

  it('accepts a new array with the same members on every render and serves the latest data', () => {
    const { result, rerender } = renderHook(({ items }) => usePresenceList([...items], getKey), {
      initialProps: { items: rows('a') },
    });

    rerender({ items: [{ id: 'a', title: 'renamed' }] });
    expect(result.current.entries).toHaveLength(1);
    expect(result.current.entries[0]?.item.title).toBe('renamed');
  });
});
