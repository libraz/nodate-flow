/**
 * Unit tests for the i18n-aware formatBytes helper.
 */

import { describe, expect, it } from 'vitest';

import { formatBytes } from '../format-bytes';

const t = (key: string): string => {
  const map: Record<string, string> = {
    'common.file_size.byte': 'B',
    'common.file_size.kilobyte': 'KB',
    'common.file_size.megabyte': 'MB',
    'common.file_size.gigabyte': 'GB',
    'common.file_size.terabyte': 'TB',
  };
  return map[key] ?? key;
};

describe('formatBytes', () => {
  it('returns bytes without fractional digits', () => {
    expect(formatBytes(0, 'en-US', t)).toBe('0 B');
    expect(formatBytes(512, 'en-US', t)).toBe('512 B');
  });

  it('promotes to KB at 1024', () => {
    expect(formatBytes(1024, 'en-US', t)).toBe('1 KB');
    expect(formatBytes(1536, 'en-US', t)).toBe('1.5 KB');
  });

  it('promotes through MB / GB / TB', () => {
    expect(formatBytes(1024 * 1024, 'en-US', t)).toBe('1 MB');
    expect(formatBytes(1024 * 1024 * 1024, 'en-US', t)).toBe('1 GB');
    expect(formatBytes(1024 ** 4, 'en-US', t)).toBe('1 TB');
  });

  it('uses the locale formatter for the value', () => {
    const ja = (key: string): string => {
      const map: Record<string, string> = {
        'common.file_size.megabyte': 'メガバイト',
      };
      return map[key] ?? key;
    };
    const formatted = formatBytes(1024 * 1024 * 2.5, 'ja-JP', ja);
    expect(formatted).toContain('メガバイト');
    expect(formatted).toContain('2.5');
  });

  it('caps at the largest known unit (TB)', () => {
    const value = formatBytes(1024 ** 5, 'en-US', t);
    expect(value).toContain('TB');
  });
});
