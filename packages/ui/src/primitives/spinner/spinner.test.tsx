import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Spinner from './spinner';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('Spinner [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders with role status and has no a11y violations', async () => {
    const { container } = render(<Spinner label="Loading" />);
    expect(screen.getByRole('status', { name: 'Loading' })).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('applies size class', () => {
    render(<Spinner label="L" size="lg" />);
    expect(screen.getByRole('status').className).toContain('lg');
  });
});
