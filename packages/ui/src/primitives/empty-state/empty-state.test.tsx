import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';

import EmptyState from './empty-state';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('EmptyState [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders title only and has no a11y violations', async () => {
    const { container } = render(<EmptyState title="Nothing here yet" />);
    expect(screen.getByText('Nothing here yet')).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('renders description and icon when provided', () => {
    render(
      <EmptyState
        icon={
          <svg data-testid="empty-icon" aria-hidden="true" width="32" height="32">
            <title>decorative</title>
          </svg>
        }
        title="Nothing here yet"
        description="Create your first task to get started."
      />,
    );
    expect(screen.getByText('Nothing here yet')).toBeDefined();
    expect(screen.getByText('Create your first task to get started.')).toBeDefined();
    expect(screen.getByTestId('empty-icon')).toBeDefined();
  });

  it('accepts ReactNode for title and description (widened API)', () => {
    render(
      <EmptyState
        title={<span data-testid="empty-title">No items</span>}
        description={<em data-testid="empty-desc">Try a different filter.</em>}
      />,
    );
    expect(screen.getByTestId('empty-title')).toBeDefined();
    expect(screen.getByTestId('empty-desc')).toBeDefined();
  });

  it('renders a custom action element and triggers its handler', () => {
    const onClick = vi.fn();
    render(
      <EmptyState
        title="Nothing here"
        action={
          <button type="button" onClick={onClick}>
            Create one
          </button>
        }
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Create one' }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
