import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Separator from './separator';

const THEMES = [
  'glass-light',
  'glass-dark',
  'modern-light',
  'modern-dark',
  'washi-light',
  'washi-dark',
  'classic-light',
  'classic-dark',
  'nothing-light',
  'nothing-dark',
] as const;

describe.each(THEMES)('Separator [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders decorative by default and has no a11y violations', async () => {
    const { container } = render(<Separator />);
    expect(await axe(container)).toHaveNoViolations();
  });

  it('exposes role separator when not decorative', () => {
    render(<Separator decorative={false} orientation="vertical" />);
    const el = screen.getByRole('separator');
    expect(el.getAttribute('aria-orientation')).toBe('vertical');
  });
});
