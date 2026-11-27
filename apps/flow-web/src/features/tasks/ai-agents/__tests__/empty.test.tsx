/**
 * AIAgentsEmpty smoke test: the empty state is a single translated line.
 */

import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it } from 'vitest';
import AIAgentsEmpty from '../empty';

describe('<AIAgentsEmpty>', () => {
  it('renders the translated empty line', () => {
    renderWithProviders(<AIAgentsEmpty />);
    expect(screen.getByText('empty.title')).toBeDefined();
  });
});
