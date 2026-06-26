/**
 * AIAgentsError smoke test.
 *
 * Asserts the thin wrapper around the shared {@link ErrorFallback}
 * primitive renders the translated title + retry button under
 * `role="alert"`, and that clicking retry calls the
 * `resetErrorBoundary` callback wired by the parent boundary.
 */

import { fireEvent, screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';
import AIAgentsError from '../error';

describe('<AIAgentsError>', () => {
  it('renders an alert with the translated title and invokes resetErrorBoundary', () => {
    const reset = vi.fn();
    renderWithProviders(
      <AIAgentsError error={new Error('boom: 503 upstream')} resetErrorBoundary={reset} />,
    );

    const alert = screen.getByRole('alert');
    expect(alert).toBeDefined();
    expect(alert.textContent).toContain('error.fetchFailed');

    const retry = screen.getByRole('button', { name: 'error.retry' });
    fireEvent.click(retry);
    expect(reset).toHaveBeenCalledTimes(1);
  });
});
