import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';

import ErrorFallback from './error-fallback';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('ErrorFallback [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders title and description, with role="alert" and no a11y violations', async () => {
    const { container } = render(
      <ErrorFallback title="Could not load" description="Network error. Please retry." />,
    );
    const alert = screen.getByRole('alert');
    expect(alert).toBeDefined();
    expect(screen.getByText('Could not load')).toBeDefined();
    expect(screen.getByText('Network error. Please retry.')).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('renders without description when omitted', () => {
    render(<ErrorFallback title="Could not load" />);
    expect(screen.getByText('Could not load')).toBeDefined();
    expect(screen.queryByText('Network error. Please retry.')).toBeNull();
  });

  it('invokes action.onClick when the retry button is clicked', () => {
    const onClick = vi.fn();
    render(<ErrorFallback title="Could not load" action={{ label: 'Retry', onClick }} />);
    const button = screen.getByRole('button', { name: 'Retry' });
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('renders a hidden span carrying the error message', () => {
    const { container } = render(
      <ErrorFallback title="Could not load" error={new Error('boom: 503 upstream')} />,
    );
    const hidden = container.querySelector('span[hidden]');
    expect(hidden).not.toBeNull();
    expect(hidden?.textContent).toBe('boom: 503 upstream');
    // hidden attribute means it is removed from the accessibility tree
    expect(hidden?.hasAttribute('hidden')).toBe(true);
  });

  it('hidden span tolerates non-Error values', () => {
    const { container, rerender } = render(<ErrorFallback title="Could not load" />);
    expect(container.querySelector('span[hidden]')?.textContent).toBe('');

    rerender(<ErrorFallback title="Could not load" error="raw string error" />);
    expect(container.querySelector('span[hidden]')?.textContent).toBe('raw string error');

    rerender(<ErrorFallback title="Could not load" error={{ message: 'object-shaped' }} />);
    expect(container.querySelector('span[hidden]')?.textContent).toBe('object-shaped');

    rerender(<ErrorFallback title="Could not load" error={null} />);
    expect(container.querySelector('span[hidden]')?.textContent).toBe('');
  });

  it('applies the inline tone class when requested', () => {
    const { container } = render(<ErrorFallback title="bare" tone="inline" />);
    expect(container.firstElementChild?.className).toMatch(/inline/);
    expect(container.firstElementChild?.className).not.toMatch(/card/);
  });

  it('defaults to the card tone', () => {
    const { container } = render(<ErrorFallback title="card by default" />);
    expect(container.firstElementChild?.className).toMatch(/card/);
  });
});
