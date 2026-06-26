/**
 * ArchivedErrorState smoke test.
 *
 * The wrapper now delegates to the shared {@link ErrorFallback}
 * primitive (per commit 5bf9eca: degrade per-section, compact intent).
 * The test asserts the translated copy lands in the DOM under
 * `role="alert"` and the retry CTA forwards to `onRetry`.
 */

import { fireEvent, screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';
import ArchivedErrorState from '../archived-error-state';

describe('<ArchivedErrorState>', () => {
  it('renders an alert with the translated title and invokes onRetry', () => {
    const onRetry = vi.fn();
    renderWithProviders(<ArchivedErrorState onRetry={onRetry} />);

    const alert = screen.getByRole('alert');
    expect(alert).toBeDefined();
    expect(alert.textContent).toContain('error.fetchFailed');

    fireEvent.click(screen.getByRole('button', { name: 'error.retry' }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});
