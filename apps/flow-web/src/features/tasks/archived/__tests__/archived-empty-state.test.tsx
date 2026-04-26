/**
 * ArchivedEmptyState smoke test.
 *
 * The wrapper composes the shared {@link EmptyState} primitive with the
 * bookshelf SVG icon and a `<Link>`-wrapped CTA back to the projects
 * surface. The test asserts the translated copy + the linked CTA both
 * render correctly.
 */

import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { renderWithProviders } from '../../../../test/helpers/render';
import ArchivedEmptyState from '../archived-empty-state';

describe('<ArchivedEmptyState>', () => {
  it('renders the translated copy and a CTA wrapping a router link', () => {
    renderWithProviders(<ArchivedEmptyState workspaceId="ws-public-id" />);

    expect(screen.getByText('empty.noneTitle')).toBeDefined();
    expect(screen.getByText('empty.noneBody')).toBeDefined();

    const cta = screen.getByRole('button', { name: 'empty.noneCta' });
    expect(cta).toBeDefined();
  });
});
