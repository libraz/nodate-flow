/**
 * PriorityError smoke test.
 *
 * Asserts the thin wrapper around the shared {@link ErrorFallback}
 * primitive renders the translated title + retry button under
 * `role="alert"`, and that clicking retry invokes
 * `resetErrorBoundary` which the wrapper bridges to a query
 * invalidation in the page.
 */

import { fireEvent, screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';
import PriorityError from '../priority-error';

describe('<PriorityError>', () => {
  it('renders an alert with the translated title and invokes resetErrorBoundary', () => {
    const reset = vi.fn();
    renderWithProviders(
      <PriorityError error={new Error('boom: 503 upstream')} resetErrorBoundary={reset} />,
    );

    const alert = screen.getByRole('alert');
    expect(alert).toBeDefined();
    expect(alert.textContent).toContain('error.fetchFailed');

    fireEvent.click(screen.getByRole('button', { name: 'error.retry' }));
    expect(reset).toHaveBeenCalledTimes(1);
  });
});
