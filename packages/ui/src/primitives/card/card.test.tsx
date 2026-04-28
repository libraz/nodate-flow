import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Card from './card';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('Card [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders children and has no a11y violations', async () => {
    const { container } = render(
      <Card>
        <p>content</p>
      </Card>,
    );
    expect(screen.getByText('content')).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('accepts elevated prop', () => {
    const { container } = render(<Card elevated>hi</Card>);
    expect(container.firstChild).toBeDefined();
  });

  it('renders as a different element when as="article" is passed', () => {
    const { container } = render(
      <Card as="article" aria-label="Card article">
        <p>body</p>
      </Card>,
    );
    const root = container.firstElementChild;
    expect(root?.tagName).toBe('ARTICLE');
    expect(root?.getAttribute('aria-label')).toBe('Card article');
  });
});
