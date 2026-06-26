/**
 * LinkedEventsEmpty smoke test.
 *
 * The wrapper composes the shared {@link EmptyState} primitive with a
 * decorative SVG icon and a "Link event" CTA. The smoke test asserts
 * the translated copy lands in the DOM and the CTA forwards clicks to
 * `onTriggerClick` so the picker can open from inside the empty state.
 */

import { fireEvent, screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';
import LinkedEventsEmpty from '../linked-events-empty';

describe('<LinkedEventsEmpty>', () => {
  it('renders the translated copy and invokes onTriggerClick', () => {
    const onTriggerClick = vi.fn();
    renderWithProviders(<LinkedEventsEmpty onTriggerClick={onTriggerClick} />);

    expect(screen.getByText('empty.title')).toBeDefined();
    expect(screen.getByText('empty.body')).toBeDefined();

    fireEvent.click(screen.getByRole('button', { name: 'empty.cta' }));
    expect(onTriggerClick).toHaveBeenCalledTimes(1);
  });
});
