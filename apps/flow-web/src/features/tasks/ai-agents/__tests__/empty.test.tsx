/**
 * AIAgentsEmpty smoke test.
 *
 * The wrapper delegates to the shared {@link EmptyState} primitive.
 * The smoke test asserts the translated title + body land in the DOM
 * so the wiring stays honest after future EmptyState changes.
 */

import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it } from 'vitest';
import AIAgentsEmpty from '../empty';

describe('<AIAgentsEmpty>', () => {
  it('renders the translated empty title and body', () => {
    renderWithProviders(<AIAgentsEmpty />);
    expect(screen.getByText('empty.title')).toBeDefined();
    expect(screen.getByText('empty.body')).toBeDefined();
  });
});
