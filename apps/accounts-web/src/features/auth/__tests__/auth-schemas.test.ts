/**
 * Schema-level tests for the auth feature folder. The signup schema's
 * password / confirm refine is verified here so the inline error wiring
 * in `routes/signup.tsx` cannot drift silently.
 */

import { describe, expect, it } from 'vitest';

import { signupSchema } from '../auth-schemas';

const baseValues = {
  email: 'user@example.test',
  displayName: 'Demo User',
  password: 'correct horse battery staple',
};

describe('signupSchema', () => {
  it('passes when password and confirmation match', () => {
    const result = signupSchema.safeParse({
      ...baseValues,
      newPasswordConfirm: baseValues.password,
    });
    expect(result.success).toBe(true);
  });

  it('fails with the canonical i18n key when the confirmation differs', () => {
    const result = signupSchema.safeParse({
      ...baseValues,
      newPasswordConfirm: 'something else entirely',
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    const issue = result.error.issues.find((entry) => entry.path[0] === 'newPasswordConfirm');
    expect(issue?.message).toBe('errors.passwords_do_not_match');
  });
});
