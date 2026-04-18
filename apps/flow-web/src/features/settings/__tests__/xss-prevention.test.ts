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

  it('clipboard writeText has error handling', () => {
    // navigator.clipboard.writeText can reject if permission is denied.
    // Each .then() must use the two-argument form .then(onFulfilled, onRejected)
    // or be followed by .catch(). The two-argument form is:
    //   .then(() => success, () => error)
    const clipboardCalls = source.match(/clipboard\.writeText/g);
    expect(clipboardCalls).not.toBeNull();

    // There should be no bare "void navigator.clipboard" (fire-and-forget)
    expect(source).not.toMatch(/void\s+navigator\.clipboard/);

    // Every .then() on clipboard calls must have a second argument (rejection handler)
    // or a subsequent .catch(). Check for 'copy_failed' error messages as evidence.
    expect(source).toContain('copy_failed');
  });
});
