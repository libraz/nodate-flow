/**
 * slugify — the generator that prefills the slug field while the user types a
 * name. Its output is handed straight to the schema built by createSlugField,
 * so every non-empty slug it produces has to pass that schema: a form must
 * never reject a value it filled in itself. The output also has to satisfy
 * what the API accepts, which is stricter than the form's regex: hyphens only
 * between alphanumerics, never at either edge.
 */

import { describe, expect, it } from 'vitest';

import { createSlugField, DNS_LABEL_MAX_LENGTH, slugify } from '../identifier';

const slug = createSlugField({
  requiredKey: 'workspaces.validation.slug_required',
  formatKey: 'workspaces.validation.slug_format',
});

const expectAccepted = (input: string): void => {
  const generated = slugify(input);
  expect(generated).not.toBe('');
  expect(slug.safeParse(generated).success).toBe(true);
  expect(generated.startsWith('-')).toBe(false);
  expect(generated.endsWith('-')).toBe(false);
};

// A name whose slug form has a hyphen exactly at the truncation boundary:
// 62 letters, a separator, then more text. Cutting before trimming leaves the
// hyphen at the end.
const nameWithSeparatorAtTheCut = `${'a'.repeat(DNS_LABEL_MAX_LENGTH - 1)} ${'b'.repeat(5)}`;

describe('slugify', () => {
  it('truncates at the DNS label limit, which is 63', () => {
    expect(DNS_LABEL_MAX_LENGTH).toBe(63);
    expect(slugify('a'.repeat(200))).toHaveLength(63);
  });

  it('produces a slug the schema accepts at the truncation boundary', () => {
    expectAccepted('a'.repeat(DNS_LABEL_MAX_LENGTH + 1));
    expectAccepted('a'.repeat(DNS_LABEL_MAX_LENGTH));
    expectAccepted('a'.repeat(DNS_LABEL_MAX_LENGTH - 1));
  });

  it('produces a slug the schema accepts for a long name with separators', () => {
    expectAccepted('The Quarterly Planning Workspace For Everyone Everywhere Always Forever');
    expectAccepted('日本語のワークスペース名 2026');
  });

  it('leaves no hyphen at the edge when the cut lands on a separator', () => {
    expectAccepted(nameWithSeparatorAtTheCut);
    expect(slugify(nameWithSeparatorAtTheCut)).toBe('a'.repeat(DNS_LABEL_MAX_LENGTH - 1));
  });

  it('lowercases, collapses separators, and trims edge dashes', () => {
    expect(slugify('  My Team!! ')).toBe('my-team');
    expect(slugify('Acme — Core/Platform')).toBe('acme-core-platform');
  });

  it('yields an empty string when the name has nothing slug-safe in it', () => {
    // The field then reports "required" rather than a format error, which is
    // the same outcome as an untouched slug input.
    expect(slugify('!!!')).toBe('');
  });
});
