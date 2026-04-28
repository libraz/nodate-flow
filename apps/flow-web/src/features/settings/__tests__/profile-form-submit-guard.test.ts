/**
 * B5 — profile-form save button must stay disabled while either the
 * react-hook-form `isSubmitting` flag OR the underlying useUpdateMe
 * mutation `isPending` flag is true. The previous code only gated on
 * `isSubmitting`, so a slow PATCH whose handler had already returned
 * (because the await resolved) re-enabled the button before the
 * mutation actually settled. A second click would queue a duplicate
 * PATCH.
 *
 * This is a source-level guard (matches the convention in
 * profile-form.test.tsx) — we assert the disabled expression contains
 * both flags so the regression cannot silently re-introduce the gap.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('profile-form save button submit guard (B5)', () => {
  const source = readFileSync(resolve(__dirname, '../profile-form.tsx'), 'utf-8');

  it('disables the submit button while update.isPending or isSubmitting is true', () => {
    // The previous form-level disabled was `disabled={isSubmitting}`.
    // We assert the new compound expression is present and the bare
    // form is gone, so a future refactor cannot silently regress.
    expect(source).toMatch(/disabled=\{[^}]*isSubmitting[^}]*update\.isPending[^}]*\}/);
  });

  it('mirrors the disabled flags into the saving label', () => {
    // The label flips to t('profile.saving') for both states so the
    // user sees feedback even if isSubmitting clears before the
    // mutation does.
    expect(source).toMatch(/isSubmitting \|\| update\.isPending\s*\?\s*t\('profile\.saving'\)/);
  });
});
