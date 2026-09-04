/**
 * createSlugField / createIdentifierField — the shared bounds behind the
 * workspace and project create dialogs. The slug limit mirrors the 63-octet
 * DNS label cap the API enforces, so it is pinned here to keep the form and
 * the server from drifting apart.
 */

import { describe, expect, it } from 'vitest';

import { createIdentifierField, createSlugField } from '../identifier';

const slug = createSlugField({
  requiredKey: 'workspaces.validation.slug_required',
  formatKey: 'workspaces.validation.slug_format',
});

describe('createSlugField', () => {
  it('accepts a slug of exactly 63 characters (the DNS label limit)', () => {
    expect(slug.safeParse('a'.repeat(63)).success).toBe(true);
  });

  it('rejects a slug of 64 characters', () => {
    expect(slug.safeParse('a'.repeat(64)).success).toBe(false);
  });

  it('rejects an empty slug', () => {
    expect(slug.safeParse('').success).toBe(false);
  });

  it('accepts lowercase letters, digits, and hyphens', () => {
    expect(slug.safeParse('my-team-2').success).toBe(true);
  });

  it('rejects uppercase letters and underscores', () => {
    expect(slug.safeParse('My-Team').success).toBe(false);
    expect(slug.safeParse('my_team').success).toBe(false);
  });

  it('honours an explicit maxLength override', () => {
    const short = createSlugField({
      requiredKey: 'workspaces.validation.slug_required',
      formatKey: 'workspaces.validation.slug_format',
      maxLength: 10,
    });
    expect(short.safeParse('a'.repeat(10)).success).toBe(true);
    expect(short.safeParse('a'.repeat(11)).success).toBe(false);
  });
});

describe('createIdentifierField', () => {
  it('accepts 1 to 5 alphanumeric characters', () => {
    const identifier = createIdentifierField();
    expect(identifier.safeParse('A').success).toBe(true);
    expect(identifier.safeParse('ABC12').success).toBe(true);
  });

  it('rejects 6 characters and non-alphanumeric input', () => {
    const identifier = createIdentifierField();
    expect(identifier.safeParse('ABCDEF').success).toBe(false);
    expect(identifier.safeParse('AB-CD').success).toBe(false);
  });
});
