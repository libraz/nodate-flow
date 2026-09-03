/**
 * The root boundary must not print `error.message` into a `<pre>` in a
 * production build. These tests assert that branch by the text it withholds,
 * not by the branch it takes: the interesting property is that a stack-ish
 * developer string never reaches a production reader, whatever shape the
 * thrown value has.
 */

import { ApiError } from '@nodate-flow/sdk';
import { describe, expect, it } from 'vitest';

import { fatalDetailMessage } from '../fatal-message';

/** Stands in for `t(code, { ns: 'errors', defaultValue: '' })`. */
const catalog: Record<string, string> = {
  'WS.COMMENT.NOT_FOUND': 'そのコメントは見つかりません',
};
const translate = (code: string): string => catalog[code] ?? '';

const LEAKY = 'Failed to fetch https://internal.example/v1/workspaces/42/tasks?token=abc';

describe('fatalDetailMessage in a production build', () => {
  it('withholds a raw Error message', () => {
    expect(fatalDetailMessage(new Error(LEAKY), translate, false)).toBe('');
  });

  it('withholds a raw thrown non-Error', () => {
    expect(fatalDetailMessage({ stack: LEAKY }, translate, false)).toBe('');
    expect(fatalDetailMessage(LEAKY, translate, false)).toBe('');
  });

  it('withholds an ApiError whose code the catalog does not know', () => {
    const err = new ApiError('NOT.IN.CATALOG', LEAKY);
    expect(fatalDetailMessage(err, translate, false)).toBe('');
  });

  it('withholds an ApiError with no code at all', () => {
    const err = new ApiError(undefined, LEAKY);
    expect(fatalDetailMessage(err, translate, false)).toBe('');
  });

  it('shows the translated catalog message, not the underlying one', () => {
    const err = new ApiError('WS.COMMENT.NOT_FOUND', LEAKY);
    const shown = fatalDetailMessage(err, translate, false);
    expect(shown).toBe('そのコメントは見つかりません');
    expect(shown).not.toContain('internal.example');
  });
});

describe('fatalDetailMessage in a development build', () => {
  it('shows the raw message so a boot failure stays debuggable', () => {
    expect(fatalDetailMessage(new Error(LEAKY), translate, true)).toBe(LEAKY);
  });

  it('stringifies a thrown non-Error', () => {
    expect(fatalDetailMessage(42, translate, true)).toBe('42');
  });

  it('still prefers the catalog message when there is one', () => {
    const err = new ApiError('WS.COMMENT.NOT_FOUND', LEAKY);
    expect(fatalDetailMessage(err, translate, true)).toBe('そのコメントは見つかりません');
  });

  it('falls back to the raw message for an uncatalogued code', () => {
    const err = new ApiError('NOT.IN.CATALOG', LEAKY);
    expect(fatalDetailMessage(err, translate, true)).toBe(LEAKY);
  });
});
