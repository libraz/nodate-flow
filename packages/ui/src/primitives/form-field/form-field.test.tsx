import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Input from '../input/input';
import FormField from './form-field';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('FormField [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('wires label + description and has no a11y violations', async () => {
    const { container } = render(
      <FormField label="Email" description="We never share it.">
        {(c) => <Input {...c} />}
      </FormField>,
    );
    const input = screen.getByLabelText('Email') as HTMLInputElement;
    const describedBy = input.getAttribute('aria-describedby');
    expect(describedBy).toBeTruthy();
    expect(input.getAttribute('aria-invalid')).toBeNull();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('applies aria-invalid when error is provided', async () => {
    const { container } = render(
      <FormField label="Name" error="Required">
        {(c) => <Input {...c} />}
      </FormField>,
    );
    const input = screen.getByLabelText('Name') as HTMLInputElement;
    expect(input.getAttribute('aria-invalid')).toBe('true');
    expect(screen.getByText('Required')).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('renders required indicator', () => {
    render(
      <FormField label="Handle" required>
        {(c) => <Input {...c} />}
      </FormField>,
    );
    expect(screen.getByText('*')).toBeDefined();
  });
});
