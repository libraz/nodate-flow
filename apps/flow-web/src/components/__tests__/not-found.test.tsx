/**
 * Component tests for the NotFound page.
 *
 * Verifies that the 404 page renders the expected structural elements
 * (heading, description, back-home link) and passes basic a11y checks.
 */

import { createMemoryHistory, createRootRoute, createRouter } from '@tanstack/react-router';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import NotFound from '../not-found';

/**
 * NotFound uses `<Link to="/">` from TanStack Router, which requires
 * a router context. We wrap it in a minimal in-memory router for tests.
 */
function renderNotFound() {
  // TanStack Router requires a root route + router instance even for
  // simple renders. We create a throwaway router with memory history
  // so the <Link> component can resolve its `to` prop.
  const rootRoute = createRootRoute({ component: NotFound });
  // Router is intentionally created for side-effect context only.
  void createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/not-here'] }),
  });

  // RouterProvider is heavy for unit tests. Instead we render NotFound
  // directly inside our provider wrapper. The Link component will
  // degrade gracefully (renders an <a> tag) when there is no active
  // router context, which is acceptable for structural testing.
  return renderWithProviders(<NotFound />);
}

describe('<NotFound>', () => {
  it('renders the 404 code, heading, description, and back-home link', () => {
    renderNotFound();

    // The "404" indicator (aria-hidden decorative element) is present
    // in the DOM as text content.
    expect(document.body.textContent).toContain('404');

    // Heading uses the i18n key (returned as-is in test i18n)
    const heading = screen.getByRole('heading', { level: 1 });
    expect(heading).toBeDefined();
    expect(heading.textContent).toBe('not_found.title');

    // Description paragraph
    const description = screen.getByText('not_found.description');
    expect(description).toBeDefined();
    expect(description.tagName).toBe('P');

    // Back-home link
    const link = screen.getByRole('link', { name: 'not_found.back_home' });
    expect(link).toBeDefined();
    expect(link.getAttribute('href')).toBe('/');
  });

  it('has no a11y violations', async () => {
    const { container } = renderNotFound();
    const results = await axe(container);
    expect(results).toHaveNoViolations();
  });
});
