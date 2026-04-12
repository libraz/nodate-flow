/**
 * Accessibility smoke test for the login page structure.
 *
 * Renders the login form using the same primitives (AuthCard, FormField,
 * Input, Button) with the same semantic structure as the real /login route,
 * verifying zero axe-core violations without pulling in TanStack Router,
 * the SDK, or other heavy runtime dependencies.
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';

import AuthCard from '../../../components/auth/auth-card';

describe('Login page a11y', () => {
  it('login form has no a11y violations', async () => {
    const { container } = render(
      <AuthCard>
        <form noValidate>
          <h1>Sign in</h1>
          <FormField label="Email" required>
            {(control) => <Input {...control} type="email" autoComplete="email" />}
          </FormField>
          <FormField label="Password" required>
            {(control) => <Input {...control} type="password" autoComplete="current-password" />}
          </FormField>
          <Button type="submit" variant="primary">
            Sign in
          </Button>
          <p>
            Don&apos;t have an account? <a href="/signup">Create one</a>
          </p>
        </form>
      </AuthCard>,
    );
    expect(await axe(container)).toHaveNoViolations();
  });

  it('login form with validation errors has no a11y violations', async () => {
    const { container } = render(
      <AuthCard>
        <form noValidate>
          <h1>Sign in</h1>
          <FormField label="Email" required error="Email is required.">
            {(control) => <Input {...control} type="email" autoComplete="email" />}
          </FormField>
          <FormField label="Password" required error="Password must be at least 8 characters.">
            {(control) => <Input {...control} type="password" autoComplete="current-password" />}
          </FormField>
          <p role="alert">Invalid email or password.</p>
          <Button type="submit" variant="primary">
            Sign in
          </Button>
        </form>
      </AuthCard>,
    );
    expect(await axe(container)).toHaveNoViolations();
  });
});
