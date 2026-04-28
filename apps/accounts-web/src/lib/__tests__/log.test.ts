/**
 * Unit tests for the lib/log helper.
 *
 * The helper exists so individual call sites do not gate on
 * import.meta.env.DEV themselves. We therefore want to pin both the
 * dev forwarding and the prod silencing branch.
 */

import { afterEach, describe, expect, it, vi } from 'vitest';

import { logError } from '../log';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
});

describe('logError', () => {
  it('forwards to console.error in dev with the [accounts-web] prefix', () => {
    vi.stubEnv('DEV', true);
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const err = new Error('boom');
    logError('Failed to fetch sessions', err);
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith('[accounts-web] Failed to fetch sessions', err);
  });

  it('is silent when DEV is false', () => {
    vi.stubEnv('DEV', false);
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    logError('production noise', new Error('do not leak'));
    expect(spy).not.toHaveBeenCalled();
  });

  it('accepts a missing err argument without throwing', () => {
    vi.stubEnv('DEV', true);
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    logError('label only');
    expect(spy).toHaveBeenCalledWith('[accounts-web] label only', undefined);
  });
});
