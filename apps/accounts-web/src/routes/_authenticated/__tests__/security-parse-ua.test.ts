/**
 * Unit tests for `parseUserAgent` — the regex-free UA token classifier
 * used by the active-sessions list. The previous implementation returned
 * the literal string "Unknown" which leaked into JA / ZH locales; the
 * fixed version returns tagged tokens so the JSX layer can resolve the
 * appropriate i18n key.
 */

import { describe, expect, it } from 'vitest';

import { parseUserAgent } from '../security';

describe('parseUserAgent', () => {
  it('returns unknown tokens for an empty UA', () => {
    const result = parseUserAgent('');
    expect(result.browser.kind).toBe('unknown');
    expect(result.os.kind).toBe('unknown');
  });

  it('returns unknown tokens for a junk UA', () => {
    const result = parseUserAgent('not-a-real-user-agent');
    expect(result.browser.kind).toBe('unknown');
    expect(result.os.kind).toBe('unknown');
  });

  it('detects Chrome on Windows', () => {
    const ua =
      'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 ' +
      '(KHTML, like Gecko) Chrome/124.0.6367.119 Safari/537.36';
    const result = parseUserAgent(ua);
    expect(result.browser).toEqual({ kind: 'name', label: 'Chrome 124' });
    expect(result.os).toEqual({ kind: 'name', label: 'Windows' });
  });

  it('detects Edge before Chrome since Edge UA embeds Chrome marker', () => {
    const ua =
      'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 ' +
      '(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36 Edg/124.0.2478.51';
    const result = parseUserAgent(ua);
    expect(result.browser).toEqual({ kind: 'name', label: 'Edge 124' });
  });

  it('detects Firefox on macOS', () => {
    const ua =
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:124.0) Gecko/20100101 Firefox/124.0';
    const result = parseUserAgent(ua);
    expect(result.browser).toEqual({ kind: 'name', label: 'Firefox 124' });
    expect(result.os).toEqual({ kind: 'name', label: 'macOS' });
  });

  it('detects Safari on iOS', () => {
    const ua =
      'Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 ' +
      '(KHTML, like Gecko) Version/17.4 Mobile/15E148 Safari/604.1';
    const result = parseUserAgent(ua);
    expect(result.browser).toEqual({ kind: 'name', label: 'Safari 17' });
    expect(result.os).toEqual({ kind: 'name', label: 'iOS' });
  });

  it('returns os unknown when only the browser is recognized', () => {
    const ua = 'Firefox/124.0';
    const result = parseUserAgent(ua);
    expect(result.browser).toEqual({ kind: 'name', label: 'Firefox 124' });
    expect(result.os.kind).toBe('unknown');
  });
});
