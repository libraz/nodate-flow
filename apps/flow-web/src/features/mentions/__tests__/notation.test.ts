/**
 * The rules that decide when an `@` is a mention and what gets written
 * when one is chosen.
 */

import { describe, expect, it } from 'vitest';

import { findMentionQuery, formatMention, insertMention } from '../notation';

const ID = '019649b0-0000-7000-8000-000000000000';

describe('findMentionQuery', () => {
  it('opens on an @ at the caret, with an empty query', () => {
    expect(findMentionQuery('Hi @', 4)).toEqual({ start: 3, query: '' });
  });

  it('carries the letters typed after the @', () => {
    expect(findMentionQuery('Hi @ann', 7)).toEqual({ start: 3, query: 'ann' });
  });

  it('gives up once a space follows the @', () => {
    expect(findMentionQuery('Hi @ann rivers', 14)).toBeNull();
  });

  it('gives up on the @ inside an email address', () => {
    expect(findMentionQuery('ann@example', 11)).toBeNull();
  });

  it('opens after a Japanese character, which no space precedes', () => {
    expect(findMentionQuery('担当は@', 4)).toEqual({ start: 3, query: '' });
  });

  it('does not reopen inside a notation it already wrote', () => {
    const body = formatMention('Ann', ID);
    expect(findMentionQuery(body, 6)).toBeNull();
  });

  it('gives up on a run too long to be a name', () => {
    const body = `@${'a'.repeat(41)}`;
    expect(findMentionQuery(body, body.length)).toBeNull();
  });
});

describe('formatMention', () => {
  it('writes the notation the backend reads', () => {
    expect(formatMention('Ann Rivers', ID)).toBe(`@[Ann Rivers](user:${ID})`);
  });

  it('escapes brackets so the link text keeps the whole name', () => {
    expect(formatMention('Ann [ops]', ID)).toBe(`@[Ann \\[ops\\]](user:${ID})`);
  });
});

describe('insertMention', () => {
  it('replaces the typed query and leaves a separating space', () => {
    expect(insertMention('Hi @an', 3, 6, 'Ann', ID)).toEqual({
      value: `Hi @[Ann](user:${ID}) `,
      caret: 3 + `@[Ann](user:${ID}) `.length,
    });
  });

  it('does not double a space already following the caret', () => {
    const result = insertMention('Hi @an rest', 3, 6, 'Ann', ID);
    expect(result.value).toBe(`Hi @[Ann](user:${ID}) rest`);
  });
});
