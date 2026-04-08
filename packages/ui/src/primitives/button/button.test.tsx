import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import Button from './button';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('Button [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders children and has no a11y violations', async () => {
    const { container } = render(<Button>Click me</Button>);
    expect(screen.getByRole('button', { name: 'Click me' })).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('defaults type to "button"', () => {
    render(<Button>Go</Button>);
    expect(screen.getByRole('button').getAttribute('type')).toBe('button');
  });

  it('fires onClick when activated', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(<Button onClick={fn}>Press</Button>);
    await user.click(screen.getByRole('button'));
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('does not fire onClick when disabled', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(
      <Button onClick={fn} disabled>
        Press
      </Button>,
    );
    await user.click(screen.getByRole('button'));
    expect(fn).not.toHaveBeenCalled();
  });

  it('supports keyboard activation via Enter', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(<Button onClick={fn}>Press</Button>);
    screen.getByRole('button').focus();
    await user.keyboard('{Enter}');
    expect(fn).toHaveBeenCalled();
  });
});
