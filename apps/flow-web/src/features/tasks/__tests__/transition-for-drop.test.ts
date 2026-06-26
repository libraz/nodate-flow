import { describe, expect, it } from 'vitest';

import {
  TASK_STATES,
  type TaskDerivedState,
  TRANSITIONS_BY_STATE,
  transitionForDrop,
} from '../api';

describe('transitionForDrop', () => {
  // ── Direct (exact) transitions ──────────────────────────────

  it.each<[TaskDerivedState, TaskDerivedState, string, TaskDerivedState]>([
    ['open', 'waiting', 'start', 'waiting'],
    ['open', 'done', 'complete', 'done'],
    ['open', 'cancelled', 'cancel', 'cancelled'],
    ['waiting', 'review', 'submit', 'review'],
    ['waiting', 'open', 'block', 'open'],
    ['waiting', 'cancelled', 'cancel', 'cancelled'],
    ['review', 'done', 'complete', 'done'],
    ['review', 'cancelled', 'cancel', 'cancelled'],
    ['cancelled', 'open', 'reopen', 'open'],
  ])('%s → %s resolves to transition=%s landing=%s', (from, to, transition, landingState) => {
    const result = transitionForDrop(from, to);
    expect(result).not.toBeNull();
    expect(result?.transition).toBe(transition);
    expect(result?.landingState).toBe(landingState);
  });

  // ── Lenient (go-back) transitions ───────────────────────────

  it.each<[TaskDerivedState, TaskDerivedState, string, TaskDerivedState]>([
    // done → open lands in waiting (reopen from done goes to waiting)
    ['done', 'open', 'reopen', 'waiting'],
    // done → waiting resolves to reopen, landing in waiting
    ['done', 'waiting', 'reopen', 'waiting'],
    // review → waiting resolves to reopen, landing in waiting
    ['review', 'waiting', 'reopen', 'waiting'],
    // review → open resolves to reopen, landing in waiting (not open)
    ['review', 'open', 'reopen', 'waiting'],
    // cancelled → waiting resolves to reopen, but actually lands in open
    ['cancelled', 'waiting', 'reopen', 'open'],
  ])('lenient: %s → %s resolves to transition=%s landing=%s', (from, to, transition, landingState) => {
    const result = transitionForDrop(from, to);
    expect(result).not.toBeNull();
    expect(result?.transition).toBe(transition);
    expect(result?.landingState).toBe(landingState);
  });

  // ── Illegal transitions (must return null) ──────────────────

  it.each<[TaskDerivedState, TaskDerivedState]>([
    // open → review has no direct transition
    ['open', 'review'],
    // done → cancelled requires two steps (reopen + cancel)
    ['done', 'cancelled'],
    // done → review is not reachable
    ['done', 'review'],
    // cancelled → review has no path
    ['cancelled', 'review'],
    // cancelled → done has no path
    ['cancelled', 'done'],
    // cancelled → cancelled is impossible
    ['cancelled', 'cancelled'],
  ])('illegal: %s → %s returns null', (from, to) => {
    // Skip same-state pairs since they're handled separately
    if (from === to) return;
    expect(transitionForDrop(from, to)).toBeNull();
  });

  // ── Same-state drops always return null ─────────────────────

  it.each(TASK_STATES)('same-state: %s → %s returns null', (state) => {
    expect(transitionForDrop(state, state)).toBeNull();
  });

  // ── Every resolved transition exists in TRANSITIONS_BY_STATE ─

  it('all resolved transitions are legal per TRANSITIONS_BY_STATE', () => {
    for (const from of TASK_STATES) {
      for (const to of TASK_STATES) {
        const result = transitionForDrop(from, to);
        if (!result) continue;
        const allowed = TRANSITIONS_BY_STATE[from];
        expect(
          allowed,
          `${from} → ${to}: transition "${result.transition}" must be in TRANSITIONS_BY_STATE["${from}"]`,
        ).toContain(result.transition);
      }
    }
  });

  // ── landingState is never the same as from ──────────────────

  it('landingState always differs from the source state', () => {
    for (const from of TASK_STATES) {
      for (const to of TASK_STATES) {
        const result = transitionForDrop(from, to);
        if (!result) continue;
        expect(result.landingState, `${from} → ${to}: landingState must not equal from`).not.toBe(
          from,
        );
      }
    }
  });

  // ── Exhaustive: every (from, to) pair is tested ─────────────

  it('covers all 25 state pairs', () => {
    let covered = 0;
    for (const from of TASK_STATES) {
      for (const to of TASK_STATES) {
        transitionForDrop(from, to); // no throw
        covered++;
      }
    }
    expect(covered).toBe(TASK_STATES.length * TASK_STATES.length);
  });
});
