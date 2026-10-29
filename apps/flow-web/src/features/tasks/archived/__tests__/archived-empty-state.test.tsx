/**
 * ArchivedEmptyState smoke test.
 *
 * The wrapper composes the shared {@link EmptyState} primitive with the
 * bookshelf SVG icon and a button-styled `<Link>` CTA back to the projects
 * surface. The test asserts the translated copy + the linked CTA both
 * render correctly, and that the CTA is one link rather than a button
 * nested inside one.
 */

import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it } from 'vitest';
import ArchivedEmptyState from '../archived-empty-state';

describe('<ArchivedEmptyState>', () => {
  it('renders the translated copy and the CTA as a single router link', () => {
    renderWithProviders(<ArchivedEmptyState workspaceId="ws-public-id" />);

    expect(screen.getByText('empty.noneTitle')).toBeDefined();
    expect(screen.getByText('empty.noneBody')).toBeDefined();

    const cta = screen.getByRole('link', { name: 'empty.noneCta' });
    expect(cta.getAttribute('href')).toContain('/workspaces/ws-public-id/projects');
    expect(screen.queryByRole('button', { name: 'empty.noneCta' })).toBeNull();
  });
});
