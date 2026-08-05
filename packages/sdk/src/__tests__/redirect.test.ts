import { describe, expect, it } from 'vitest';

import { isSafeRedirect, parseAllowedOrigins } from '../redirect';

const ORIGIN = 'https://accounts.nodate-flow.dev';
const PARTNER = 'https://acme.nodate-flow.dev';

describe('isSafeRedirect', () => {
  it('accepts paths that resolve back to the current origin', () => {
    expect(isSafeRedirect('/workspaces/abc?x=1#y', ORIGIN)).toBe(true);
    expect(isSafeRedirect('/', ORIGIN)).toBe(true);
    expect(isSafeRedirect(`${ORIGIN}/workspaces/abc`, ORIGIN)).toBe(true);
  });

  it('rejects backslash-escaped paths that resolve to a foreign origin', () => {
    // The URL spec treats a backslash as a path separator for http(s),
    // so these are protocol-relative URLs in disguise.
    expect(isSafeRedirect('/\\evil.com', ORIGIN)).toBe(false);
    expect(isSafeRedirect('/\\/evil.com', ORIGIN)).toBe(false);
    expect(isSafeRedirect('\\\\evil.com', ORIGIN)).toBe(false);
  });

  it('rejects percent-encoded backslashes', () => {
    // Same-origin once resolved, but one more round of decoding
    // downstream would turn it into a foreign origin.
    expect(isSafeRedirect('/%5Cevil.com', ORIGIN)).toBe(false);
    expect(isSafeRedirect('/%5cevil.com', ORIGIN)).toBe(false);
  });

  it('rejects protocol-relative and absolute foreign origins', () => {
    expect(isSafeRedirect('//evil.com', ORIGIN)).toBe(false);
    expect(isSafeRedirect('https://evil.com/', ORIGIN)).toBe(false);
    expect(isSafeRedirect('https://evil.com/workspaces/abc', ORIGIN)).toBe(false);
  });

  it('rejects a foreign origin that only prefixes the current one', () => {
    expect(isSafeRedirect('https://accounts.nodate-flow.dev.evil.com/', ORIGIN)).toBe(false);
    expect(isSafeRedirect('https://evil.com/?x=https://accounts.nodate-flow.dev', ORIGIN)).toBe(
      false,
    );
  });

  it('rejects non-http schemes', () => {
    expect(isSafeRedirect('javascript:alert(1)', ORIGIN)).toBe(false);
    expect(isSafeRedirect('data:text/html,<script></script>', ORIGIN)).toBe(false);
    expect(isSafeRedirect('  javascript:alert(1)', ORIGIN)).toBe(false);
  });

  it('accepts allowlisted cross-origin targets', () => {
    expect(isSafeRedirect(`${PARTNER}/tasks`, ORIGIN, [PARTNER])).toBe(true);
    // Allowlist entries may carry a path or trailing slash; only the
    // origin part is compared.
    expect(isSafeRedirect(`${PARTNER}/tasks`, ORIGIN, [`${PARTNER}/`])).toBe(true);
  });

  it('rejects cross-origin targets outside the allowlist', () => {
    expect(isSafeRedirect('https://evil.com/tasks', ORIGIN, [PARTNER])).toBe(false);
    // The scheme and port are part of the origin.
    expect(isSafeRedirect(`http://acme.nodate-flow.dev/tasks`, ORIGIN, [PARTNER])).toBe(false);
    expect(isSafeRedirect('http://localhost:5173/tasks', ORIGIN, ['http://localhost:5175'])).toBe(
      false,
    );
  });

  it('treats a port difference on the same host as cross-origin', () => {
    const dev = 'http://localhost:5175';
    expect(isSafeRedirect('http://localhost:5173/tasks', dev)).toBe(false);
    expect(isSafeRedirect('http://localhost:5173/tasks', dev, ['http://localhost:5173'])).toBe(
      true,
    );
  });

  it('rejects everything when the app origin is not an absolute http(s) URL', () => {
    expect(isSafeRedirect('/tasks', '')).toBe(false);
    expect(isSafeRedirect('/tasks', 'not-a-url')).toBe(false);
    expect(isSafeRedirect('/tasks', 'null')).toBe(false);
  });
});

describe('parseAllowedOrigins', () => {
  it('normalizes a comma-separated list to origins', () => {
    expect(parseAllowedOrigins(` ${PARTNER}/tasks , http://localhost:5173 `)).toEqual([
      PARTNER,
      'http://localhost:5173',
    ]);
  });

  it('drops empty, duplicate and non-http entries', () => {
    expect(parseAllowedOrigins('')).toEqual([]);
    expect(parseAllowedOrigins(undefined)).toEqual([]);
    expect(parseAllowedOrigins(`${PARTNER},,${PARTNER}/other`)).toEqual([PARTNER]);
    expect(parseAllowedOrigins('acme.nodate-flow.dev,javascript:alert(1)')).toEqual([]);
  });
});
