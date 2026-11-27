import { QueryClient, QueryObserver } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { tasksKeys } from '../../tasks/api';
import { flushInvalidations } from '../flush-invalidations';

interface Seeded {
  client: QueryClient;
  fetches: Record<string, ReturnType<typeof vi.fn>>;
  setRoot: (value: unknown) => void;
}

/** Observe a query the way a mounted component would. */
function observe(client: QueryClient, key: readonly unknown[], fn: () => unknown): void {
  new QueryObserver(client, { queryKey: key, queryFn: async () => fn() }).subscribe(() => {});
}

async function seed(id: string): Promise<Seeded> {
  const client = new QueryClient();
  let root: unknown = { id, title: 'a', updatedAt: 1 };
  const fetches = {
    root: vi.fn(() => ({ ...(root as object) })),
    comments: vi.fn(() => ['c']),
    workflow: vi.fn(() => ({ w: 1 })),
    list: vi.fn(() => [{ id }]),
  };
  const specs: [readonly unknown[], () => unknown][] = [
    [tasksKeys.detail(id), fetches.root],
    [tasksKeys.comments(id), fetches.comments],
    [tasksKeys.workflow(id), fetches.workflow],
    [tasksKeys.list('p1'), fetches.list],
  ];
  for (const [key, fn] of specs) {
    await client.fetchQuery({ queryKey: key, queryFn: async () => fn() });
    observe(client, key, fn);
  }
  for (const fn of Object.values(fetches)) fn.mockClear();
  return { client, fetches, setRoot: (v) => (root = v) };
}

describe('flushInvalidations for tasks.all', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('refetches roots and lists but not sub-resources when the root payload is unchanged', async () => {
    const { client, fetches } = await seed('t1');
    await flushInvalidations(client, [tasksKeys.all], false);
    expect(fetches.root).toHaveBeenCalledTimes(1);
    expect(fetches.list).toHaveBeenCalledTimes(1);
    expect(fetches.comments).not.toHaveBeenCalled();
    expect(fetches.workflow).not.toHaveBeenCalled();
    expect(client.getQueryState(tasksKeys.comments('t1'))?.isInvalidated).toBe(true);
  });

  it('refetches sub-resources of a task whose root payload changed', async () => {
    const { client, fetches, setRoot } = await seed('t1');
    setRoot({ id: 't1', title: 'b', updatedAt: 2 });
    await flushInvalidations(client, [tasksKeys.all], false);
    expect(fetches.comments).toHaveBeenCalledTimes(1);
    expect(fetches.workflow).toHaveBeenCalledTimes(1);
  });

  it('only marks everything stale while hidden', async () => {
    const { client, fetches, setRoot } = await seed('t1');
    setRoot({ id: 't1', title: 'b', updatedAt: 2 });
    await flushInvalidations(client, [tasksKeys.all], true);
    for (const fn of Object.values(fetches)) expect(fn).not.toHaveBeenCalled();
    expect(client.getQueryState(tasksKeys.detail('t1'))?.isInvalidated).toBe(true);
  });
});
