/**
 * Verify that URL-based XSS vectors are mitigated in settings panels.
 *
 * Bugs fixed:
 * - integrations-panel.tsx: authorizeUrl used in window.location.assign()
 *   without scheme validation (could execute javascript: URLs)
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

describe('CopyButton clipboard failure handling', () => {
  it('CopyButton catches a rejected clipboard write', () => {
    const button = readFileSync(resolve(__dirname, '../copy-button.tsx'), 'utf-8');
    expect(button).toContain('await navigator.clipboard.writeText(value)');
    expect(button).toContain('catch (err)');
    expect(button).not.toMatch(/void\s+navigator\.clipboard/);
  });
});
