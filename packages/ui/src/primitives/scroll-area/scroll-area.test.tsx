import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import ScrollArea from './scroll-area';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('ScrollArea [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders children and has no a11y violations', async () => {
    const { container } = render(
      <ScrollArea aria-label="content" maxBlockSize={200}>
        <p>line 1</p>
        <p>line 2</p>
      </ScrollArea>,
    );
    expect(screen.getByText('line 1')).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('applies maxBlockSize as inline style', () => {
    render(
      <ScrollArea aria-label="content" maxBlockSize="120px" data-testid="sa">
        x
      </ScrollArea>,
    );
    const el = screen.getByTestId('sa');
    expect(el.style.maxBlockSize).toBe('120px');
  });

  it('is focusable via tabindex', () => {
    render(
      <ScrollArea aria-label="content" data-testid="sa">
        x
      </ScrollArea>,
    );
    const el = screen.getByTestId('sa');
    el.focus();
    expect(document.activeElement).toBe(el);
  });
});
