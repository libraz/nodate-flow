/**
 * Inline subtask nesting for the project task lists.
 *
 * The list and the spreadsheet render one flat row array; this hook turns the
 * loaded top-level tasks plus the expanded ids into that array, children
 * directly beneath their parent, each tagged with its depth. Children are read
 * from the `tasksKeys.children` cache that `SubtaskLoaders` fills, so a row
 * only appears once its parent is expanded and its children have loaded.
 */

import { useQueries } from '@tanstack/react-query';
import { useState } from 'react';
import { type SubtasksResult, type TaskListItem, tasksKeys } from './api';

/** One visible row of the nested list. */
export interface TreeRow {
  task: TaskListItem;
  /** 0 for a top-level row, one more per level of nesting. */
  depth: number;
  /** Position among the top-level rows; -1 for a nested row. */
  rootIndex: number;
  /** False in the flat (filtered) mode, where no row shows a disclosure. */
  tree: boolean;
  /** The task has direct subtasks to show. */
  expandable: boolean;
  expanded: boolean;
  /** Expanded, and its children have not arrived yet. */
  loading: boolean;
}

/**
 * Children of a parent as the cache knows them: the rows, `undefined` while
 * they are still loading, `null` when the load failed.
 */
export type ChildrenOf = (parentId: string) => readonly TaskListItem[] | undefined | null;

export interface FlattenedTree {
  rows: TreeRow[];
  /** Expanded parents that are on screen, whose children must be loaded. */
  visibleExpandedIds: string[];
}

/** Flat mode: every task is a plain top-level row. */
export function flattenFlat(roots: readonly TaskListItem[]): FlattenedTree {
  return {
    rows: roots.map((task, rootIndex) => ({
      task,
      depth: 0,
      rootIndex,
      tree: false,
      expandable: false,
      expanded: false,
      loading: false,
    })),
    visibleExpandedIds: [],
  };
}

/** Tree mode: expanded parents are followed by their children, recursively. */
export function flattenSubtaskTree(
  roots: readonly TaskListItem[],
  expandedIds: ReadonlySet<string>,
  childrenOf: ChildrenOf,
): FlattenedTree {
  const rows: TreeRow[] = [];
  const visibleExpandedIds: string[] = [];

  const walk = (tasks: readonly TaskListItem[], depth: number): void => {
    tasks.forEach((task, index) => {
      const expandable = task.subtaskCount > 0;
      const expanded = expandable && expandedIds.has(task.id);
      const children = expanded ? childrenOf(task.id) : null;
      rows.push({
        task,
        depth,
        rootIndex: depth === 0 ? index : -1,
        tree: true,
        expandable,
        expanded,
        loading: expanded && children === undefined,
      });
      if (!expanded) return;
      visibleExpandedIds.push(task.id);
      if (children) walk(children, depth + 1);
    });
  };
  walk(roots, 0);
  return { rows, visibleExpandedIds };
}

export interface SubtaskTree extends FlattenedTree {
  toggle: (taskId: string) => void;
}

/**
 * @param roots - the loaded top-level tasks (or, in flat mode, every task)
 * @param tree - nest subtasks under their parents; false keeps the flat list
 */
export function useSubtaskTree(roots: readonly TaskListItem[], tree: boolean): SubtaskTree {
  const [expandedIds, setExpandedIds] = useState<ReadonlySet<string>>(() => new Set());

  // Read-only observers on the children caches; the fetch is owned by SubtaskLoaders.
  const childrenById = useQueries({
    queries: (tree ? [...expandedIds] : []).map((id) => ({
      queryKey: tasksKeys.children(id),
      enabled: false,
    })),
    combine: (results) => {
      const byId = new Map<string, readonly TaskListItem[] | null>();
      const ids = [...expandedIds];
      results.forEach((result, i) => {
        const id = ids[i];
        if (id === undefined) return;
        const data = result.data as SubtasksResult | undefined;
        if (data) byId.set(id, data.tasks);
        else if (result.isError) byId.set(id, null);
      });
      return byId;
    },
  });

  const toggle = (taskId: string): void => {
    setExpandedIds((prev) => {
      const next = new Set(prev);
      if (!next.delete(taskId)) next.add(taskId);
      return next;
    });
  };

  const flattened = tree
    ? flattenSubtaskTree(roots, expandedIds, (id) => childrenById.get(id))
    : flattenFlat(roots);
  return { ...flattened, toggle };
}
