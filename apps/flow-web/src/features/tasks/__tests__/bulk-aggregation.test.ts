/**
 * Behavioural test for the Promise.allSettled aggregation pattern used
 * by the task list / spreadsheet bulk action bars. The algorithm is
 * inlined in both call sites; this test pins the contract so a future
 * refactor (e.g. extracting a helper) keeps the same observable
 * behaviour.
 */

import { describe, expect, it } from 'vitest';

interface Aggregate {
  failed: number;
  total: number;
  outcome: 'success' | 'partial' | 'fail';
}

function aggregate(results: PromiseSettledResult<unknown>[]): Aggregate {
  const failed = results.filter((r) => r.status === 'rejected').length;
  const total = results.length;
  let outcome: Aggregate['outcome'];
  if (failed === 0) outcome = 'success';
  else if (failed === total) outcome = 'fail';
  else outcome = 'partial';
  return { failed, total, outcome };
}

async function runBulk<T>(items: T[], op: (item: T) => Promise<void>): Promise<Aggregate> {
  const settled = await Promise.allSettled(items.map((item) => op(item)));
  return aggregate(settled);
}

describe('bulk allSettled aggregation', () => {
  it('reports success when every item resolves', async () => {
    const result = await runBulk([1, 2, 3], () => Promise.resolve());
    expect(result).toEqual({ failed: 0, total: 3, outcome: 'success' });
  });

  it('reports total failure when every item rejects', async () => {
    const result = await runBulk([1, 2], () => Promise.reject(new Error('x')));
    expect(result).toEqual({ failed: 2, total: 2, outcome: 'fail' });
  });

  it('reports partial failure when some items reject', async () => {
    const result = await runBulk([1, 2, 3, 4, 5], (n) =>
      n % 2 === 0 ? Promise.reject(new Error('x')) : Promise.resolve(),
    );
    expect(result).toEqual({ failed: 2, total: 5, outcome: 'partial' });
  });

  it('does not abort the batch on a single rejection', async () => {
    let resolvedCount = 0;
    const op = (n: number): Promise<void> =>
      new Promise((resolve, reject) => {
        if (n === 2) reject(new Error('boom'));
        else {
          resolvedCount += 1;
          resolve();
        }
      });
    await runBulk([1, 2, 3], op);
    expect(resolvedCount).toBe(2);
  });
});
