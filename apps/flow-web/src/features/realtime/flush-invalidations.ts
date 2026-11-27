/**
 * Applies a coalesced batch of key prefixes to the query cache.
 *
 * The stream carries no task id, so `tasksKeys.all` cannot say which task
 * changed. Task-detail sub-resources are therefore only marked stale; they are
 * refetched actively for a task whose detail root actually changed.
 */

import type { Query, QueryClient } from '@tanstack/react-query';

import { tasksKeys } from '../tasks/api';
import type { QueryKeyPrefix } from './invalidation-coalescer';

const DETAIL_ROOT_LENGTH = tasksKeys.detail('').length;

function isTaskDetailKey(key: QueryKeyPrefix): boolean {
  return key[0] === tasksKeys.all[0] && key[1] === 'detail';
}

function isDetailSubResource(key: QueryKeyPrefix): boolean {
  return isTaskDetailKey(key) && key.length > DETAIL_ROOT_LENGTH;
}

function isTasksAll(key: QueryKeyPrefix): boolean {
  return key.length === tasksKeys.all.length && key[0] === tasksKeys.all[0];
}

function notDetailSubResource(query: Query): boolean {
  return !isDetailSubResource(query.queryKey);
}

function detailRootSnapshot(queryClient: QueryClient): Map<string, unknown> {
  const snapshot = new Map<string, unknown>();
  for (const query of queryClient
    .getQueryCache()
    .findAll({ queryKey: [...tasksKeys.all, 'detail'] })) {
    if (query.queryKey.length === DETAIL_ROOT_LENGTH) {
      snapshot.set(String(query.queryKey[2]), query.state.data);
    }
  }
  return snapshot;
}

async function invalidateTasks(queryClient: QueryClient, hidden: boolean): Promise<void> {
  const before = detailRootSnapshot(queryClient);

  void queryClient.invalidateQueries({
    queryKey: tasksKeys.all,
    predicate: (query) => isDetailSubResource(query.queryKey),
    refetchType: 'none',
  });
  await queryClient.invalidateQueries({
    queryKey: tasksKeys.all,
    predicate: notDetailSubResource,
    refetchType: hidden ? 'none' : 'active',
  });
  if (hidden) return;

  // Structural sharing keeps the data reference when the payload is unchanged.
  const changed = [...before].filter(
    ([id, data]) => queryClient.getQueryData(tasksKeys.detail(id)) !== data,
  );
  await Promise.all(
    changed.map(([id]) =>
      queryClient.invalidateQueries({
        queryKey: tasksKeys.detail(id),
        predicate: (query) => isDetailSubResource(query.queryKey),
        refetchType: 'active',
      }),
    ),
  );
}

/** Invalidate each prefix; a hidden tab only marks queries stale. */
export async function flushInvalidations(
  queryClient: QueryClient,
  keys: readonly QueryKeyPrefix[],
  hidden: boolean,
): Promise<void> {
  await Promise.all(
    keys.map((key) =>
      isTasksAll(key)
        ? invalidateTasks(queryClient, hidden)
        : queryClient.invalidateQueries({ queryKey: key, refetchType: hidden ? 'none' : 'active' }),
    ),
  );
}
