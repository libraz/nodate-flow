import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Badge from './badge';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('Badge [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders and has no a11y violations', async () => {
    const { container } = render(<Badge>new</Badge>);
    expect(screen.getByText('new')).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('applies tone class', () => {
    render(<Badge tone="success">ok</Badge>);
    expect(screen.getByText('ok').className).toContain('success');
  });
});
