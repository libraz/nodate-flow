/**
 * Verify that URL-based XSS vectors are mitigated in settings panels.
 *
 * Bugs fixed:
 * - integrations-panel.tsx: authorizeUrl used in window.location.assign()
 *   without scheme validation (could execute javascript: URLs)
 * - totp-panel.tsx: otpauthUrl rendered in <a href> without scheme check
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('integrations-panel XSS prevention', () => {
  const source = readFileSync(resolve(__dirname, '../integrations-panel.tsx'), 'utf-8');

  it('validates URL scheme before window.location.assign()', () => {
    // Must construct a URL object to validate the scheme
    expect(source).toContain('new URL(authorizeUrl)');
    expect(source).toContain("url.protocol !== 'https:'");
  });

  it('does not use raw authorizeUrl in location.assign', () => {
    // Should use url.href (sanitized) not the raw string
    expect(source).toContain('window.location.assign(url.href)');
    expect(source).not.toContain('window.location.assign(authorizeUrl)');
  });
});

describe('totp-panel XSS prevention', () => {
  const source = readFileSync(resolve(__dirname, '../totp-panel.tsx'), 'utf-8');

  it('validates otpauthUrl scheme before rendering in href', () => {
    // Must check that the URL starts with "otpauth:" before using it
    expect(source).toContain("startsWith('otpauth:')");
  });

  it('clipboard writes go through CopyButton with a failure handler', () => {
    // navigator.clipboard.writeText can reject if permission is denied.
    // The panel delegates every write to CopyButton and hands it an
    // onError that reports the failure.
    expect(source).not.toMatch(/clipboard\.writeText/);
    expect(source).toContain('<CopyButton');
    expect(source).toContain('onError=');
    expect(source).toContain('copy_failed');
  });

  it('CopyButton catches a rejected clipboard write', () => {
    const button = readFileSync(resolve(__dirname, '../copy-button.tsx'), 'utf-8');
    expect(button).toContain('await navigator.clipboard.writeText(value)');
    expect(button).toContain('catch (err)');
    expect(button).not.toMatch(/void\s+navigator\.clipboard/);
  });
});
