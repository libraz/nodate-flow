/**
 * MetricsError smoke test.
 *
 * Verifies the thin wrapper around the shared {@link ErrorFallback}
 * primitive renders the translated title + retry button under
 * `role="alert"`, and clicking retry invokes the `onRetry` prop the
 * dashboard wires to a query refetch.
 */

import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from '../../../test/helpers/render';
import MetricsError from '../metrics-error';

describe('<MetricsError>', () => {
  it('renders an alert with the translated title and invokes onRetry', () => {
    const onRetry = vi.fn();
    renderWithProviders(<MetricsError onRetry={onRetry} />);

    const alert = screen.getByRole('alert');
    expect(alert).toBeDefined();
    expect(alert.textContent).toContain('error.fetchFailed');

    fireEvent.click(screen.getByRole('button', { name: 'error.retry' }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});
