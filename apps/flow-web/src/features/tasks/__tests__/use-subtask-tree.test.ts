/**
 * The flatten step behind the nested list: children sit directly beneath an
 * expanded parent, nesting is recursive with one depth per level, and the flat
 * (filtered) mode never nests.
 */

import { describe, expect, it } from 'vitest';

import type { TaskListItem } from '../api';
import { flattenFlat, flattenSubtaskTree } from '../use-subtask-tree';

function task(id: string, subtaskCount = 0, parentTaskId?: string): TaskListItem {
  return { id, title: id, subtaskCount, parentTaskId } as unknown as TaskListItem;
}

const roots = [task('A', 2), task('B'), task('C', 1)];
const kids: Record<string, TaskListItem[]> = {
  A: [task('A1', 1, 'A'), task('A2', 0, 'A')],
  A1: [task('A1a', 0, 'A1')],
  C: [task('C1', 0, 'C')],
};
const childrenOf = (id: string): TaskListItem[] | undefined => kids[id];
const ids = (rows: { task: TaskListItem }[]): string[] => rows.map((r) => r.task.id);

describe('flattenSubtaskTree', () => {
  it('lists only the top-level rows while nothing is expanded', () => {
    const { rows, visibleExpandedIds } = flattenSubtaskTree(roots, new Set(), childrenOf);
    expect(ids(rows)).toEqual(['A', 'B', 'C']);
    expect(rows.map((r) => r.expandable)).toEqual([true, false, true]);
    expect(visibleExpandedIds).toEqual([]);
  });

  it('places children directly beneath their parent, one depth deeper', () => {
    const { rows } = flattenSubtaskTree(roots, new Set(['A']), childrenOf);
    expect(ids(rows)).toEqual(['A', 'A1', 'A2', 'B', 'C']);
    expect(rows.map((r) => r.depth)).toEqual([0, 1, 1, 0, 0]);
    expect(rows.map((r) => r.rootIndex)).toEqual([0, -1, -1, 1, 2]);
  });

  it('nests recursively', () => {
    const { rows, visibleExpandedIds } = flattenSubtaskTree(
      roots,
      new Set(['A', 'A1', 'C']),
      childrenOf,
    );
    expect(ids(rows)).toEqual(['A', 'A1', 'A1a', 'A2', 'B', 'C', 'C1']);
    expect(rows.map((r) => r.depth)).toEqual([0, 1, 2, 1, 0, 0, 1]);
    expect(visibleExpandedIds).toEqual(['A', 'A1', 'C']);
  });

  it('hides an expanded descendant under a collapsed ancestor', () => {
    const { rows, visibleExpandedIds } = flattenSubtaskTree(roots, new Set(['A1']), childrenOf);
    expect(ids(rows)).toEqual(['A', 'B', 'C']);
    expect(visibleExpandedIds).toEqual([]);
  });

  it('marks an expanded parent as loading until its children arrive', () => {
    const { rows } = flattenSubtaskTree(roots, new Set(['A']), () => undefined);
    expect(rows[0]).toMatchObject({ expanded: true, loading: true });
    expect(ids(rows)).toEqual(['A', 'B', 'C']);
  });

  it('shows no children and no spinner for a parent whose load failed', () => {
    const { rows } = flattenSubtaskTree(roots, new Set(['A']), () => null);
    expect(rows[0]).toMatchObject({ expanded: true, loading: false });
    expect(ids(rows)).toEqual(['A', 'B', 'C']);
  });

  it('never expands a task without subtasks', () => {
    const { rows } = flattenSubtaskTree(roots, new Set(['B']), childrenOf);
    expect(rows[1]).toMatchObject({ expanded: false, expandable: false });
  });
});

describe('flattenFlat', () => {
  it('keeps every task as a plain top-level row without a disclosure', () => {
    const flat = [task('A', 2), task('A1', 0, 'A')];
    const { rows, visibleExpandedIds } = flattenFlat(flat);
    expect(ids(rows)).toEqual(['A', 'A1']);
    expect(rows.every((r) => !r.tree && !r.expandable && r.depth === 0)).toBe(true);
    expect(rows.map((r) => r.rootIndex)).toEqual([0, 1]);
    expect(visibleExpandedIds).toEqual([]);
  });
});
