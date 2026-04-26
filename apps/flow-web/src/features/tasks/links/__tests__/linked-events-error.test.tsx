/**
 * LinkedEventsError smoke test.
 *
 * Verifies the thin wrapper around {@link ErrorFallback}:
 *  - exposes `role="alert"` with the translated copy,
 *  - prefers `resetErrorBoundary` over the legacy `onRetry` alias,
 *  - falls back to `onRetry` when only that prop is supplied.
 */

import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from '../../../../test/helpers/render';
import LinkedEventsError from '../linked-events-error';

describe('<LinkedEventsError>', () => {
  it('renders an alert and invokes resetErrorBoundary on retry', () => {
    const reset = vi.fn();
    renderWithProviders(<LinkedEventsError error={new Error('boom')} resetErrorBoundary={reset} />);

    const alert = screen.getByRole('alert');
    expect(alert).toBeDefined();
    expect(alert.textContent).toContain('error.fetchFailed');

    fireEvent.click(screen.getByRole('button', { name: 'error.retry' }));
    expect(reset).toHaveBeenCalledTimes(1);
  });

  it('falls back to onRetry when resetErrorBoundary is not supplied', () => {
    const onRetry = vi.fn();
    renderWithProviders(<LinkedEventsError error={new Error('boom')} onRetry={onRetry} />);

    fireEvent.click(screen.getByRole('button', { name: 'error.retry' }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});
