import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Avatar from './avatar';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('Avatar [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders initials fallback and has no a11y violations', async () => {
    const { container } = render(<Avatar alt="Alice" initials="AL" />);
    expect(screen.getByRole('img', { name: 'Alice' })).toBeDefined();
    expect(screen.getByText('AL')).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('renders image when src is provided', async () => {
    const { container } = render(<Avatar alt="Bob" src="data:image/png;base64,iVBORw0KGgo=" />);
    expect(container.querySelector('img')).not.toBeNull();
    expect(await axe(container)).toHaveNoViolations();
  });
});
