import { render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Skeleton from './skeleton';

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

describe.each(THEMES)('Skeleton [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders hidden from AT and has no a11y violations', async () => {
    const { container } = render(<Skeleton />);
    expect((container.firstChild as HTMLElement).getAttribute('aria-hidden')).toBe('true');
    expect(await axe(container)).toHaveNoViolations();
  });
});
