/**
 * Verify that useTransitionTask invalidates the replay query so the
 * Replay diagnostic panel refreshes without a manual click after every
 * state transition.
 *
 * Bug fixed: onSettled invalidated tasksKeys.detail + timelineKeys +
 * tasksKeys.list but NOT replayKeys.task(id). The replay panel stayed on
 * the pre-transition result (e.g. "open / open / ✓") until the user hit
 * Refresh inside the widget, creating a phantom-stale reading.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

import { replayKeys } from '../../timeline/replay-api';
import { tasksKeys } from '../api';

describe('useTransitionTask replay invalidation', () => {
  const source = readFileSync(resolve(__dirname, '../api.ts'), 'utf-8');

  const fnStart = source.indexOf('export function useTransitionTask()');
  const fnEnd = source.indexOf('\n}', fnStart) + 2;
  const fnBody = source.slice(fnStart, fnEnd);

  it('onSettled invalidates replayKeys.task(vars.id)', () => {
    const settledStart = fnBody.indexOf('onSettled');
    expect(settledStart).toBeGreaterThan(-1);
    const onSettledBody = fnBody.slice(settledStart);

    expect(onSettledBody).toContain('replayKeys.task(vars.id)');
  });

  it('imports replayKeys from the replay-api module', () => {
    expect(source).toMatch(
      /import\s+\{[^}]*replayKeys[^}]*\}\s+from\s+['"]\.\.\/timeline\/replay-api['"]/,
    );
  });
});

describe('replayKeys factory shape', () => {
  it('task key is a prefix-matchable array rooted at "replay"', () => {
    const key = replayKeys.task('abc-123');
    expect(key[0]).toBe('replay');
    expect(key).toContain('abc-123');
  });

  it('does not collide with tasksKeys.detail', () => {
    const replay = replayKeys.task('t1') as readonly unknown[];
    const detail = tasksKeys.detail('t1') as readonly unknown[];
    expect(replay[0]).not.toBe(detail[0]);
  });
});
