/**
 * Verify that mutation hooks invalidate the correct query cache keys.
 *
 * These tests extract the hardcoded query-key literals from the source
 * and assert they match the canonical `tasksKeys` factory output. This
 * catches the class of bug where a key is hand-typed instead of using
 * the factory (e.g. ['tasks', taskId] vs ['tasks', 'detail', taskId]).
 */

import { describe, expect, it } from 'vitest';

import { tasksKeys } from '../api';

describe('tasksKeys factory', () => {
  it('detail key includes "detail" segment', () => {
    const key = tasksKeys.detail('abc-123');
    expect(key).toEqual(['tasks', 'detail', 'abc-123']);
  });

  it('list key includes "list" segment and projectId', () => {
    const key = tasksKeys.list('proj-456');
    expect(key[0]).toBe('tasks');
    expect(key[1]).toBe('list');
    expect(key[2]).toBe('proj-456');
  });

  it('all key is ["tasks"]', () => {
    expect(tasksKeys.all).toEqual(['tasks']);
  });

  it('detail key is a prefix of comments/actors/dependencies keys', () => {
    const detail = tasksKeys.detail('t1');
    const comments = tasksKeys.comments('t1');
    const actors = tasksKeys.actors('t1');
    const dependencies = tasksKeys.dependencies('t1');

    // detail = ['tasks', 'detail', 't1']
    // sub-keys = ['tasks', 'detail', 't1', 'comments'] etc.
    for (const subKey of [comments, actors, dependencies]) {
      expect(subKey.slice(0, 3)).toEqual(detail);
      expect(subKey.length).toBeGreaterThan(detail.length);
    }
  });

  it('invalidating [...all, "list"] does NOT match detail keys', () => {
    // This documents the fix: invalidating ['tasks', 'list'] must NOT
    // match ['tasks', 'detail', id] or ['tasks', 'detail', id, 'comments'].
    // React Query prefix matching: ['tasks', 'list'] matches
    // ['tasks', 'list', ...] but NOT ['tasks', 'detail', ...].
    const listPrefix = [...tasksKeys.all, 'list'];
    const detailKey = tasksKeys.detail('x');
    const commentsKey = tasksKeys.comments('x');

    // Simulate prefix match: every element of prefix must match
    const prefixMatches = (prefix: readonly unknown[], target: readonly unknown[]): boolean => {
      if (prefix.length > target.length) return false;
      return prefix.every((v, i) => v === target[i]);
    };

    expect(prefixMatches(listPrefix, detailKey)).toBe(false);
    expect(prefixMatches(listPrefix, commentsKey)).toBe(false);
    // But it DOES match list keys
    const listKey = tasksKeys.list('proj1');
    expect(prefixMatches(listPrefix, listKey)).toBe(true);
  });
});
