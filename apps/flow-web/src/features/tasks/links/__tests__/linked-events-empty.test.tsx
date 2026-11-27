/**
 * LinkedEventsEmpty smoke test: the empty state is a single translated line.
 */

import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it } from 'vitest';
import LinkedEventsEmpty from '../linked-events-empty';

describe('<LinkedEventsEmpty>', () => {
  it('renders the translated empty line', () => {
    renderWithProviders(<LinkedEventsEmpty />);
    expect(screen.getByText('empty.title')).toBeDefined();
    expect(screen.queryByRole('button')).toBeNull();
  });
});
