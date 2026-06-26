/**
 * PublicPageLayout — structural and a11y coverage. Verifies the layout
 * always emits a `<main>` landmark, opt-in regions render only when
 * requested, and the busy flag forwards to `aria-busy` so assistive
 * tech can announce skeleton state.
 */

import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import PublicPageLayout from '../public-page-layout';

describe('<PublicPageLayout>', () => {
  it('renders children inside a <main> landmark', () => {
    renderWithProviders(
      <PublicPageLayout>
        <p>hello world</p>
      </PublicPageLayout>,
    );

    const main = screen.getByRole('main');
    expect(main.textContent).toBe('hello world');
    expect(main.tagName).toBe('MAIN');
  });

  it('forwards `busy` to aria-busy on the main landmark', () => {
    renderWithProviders(
      <PublicPageLayout busy>
        <p>loading</p>
      </PublicPageLayout>,
    );

    expect(screen.getByRole('main').getAttribute('aria-busy')).toBe('true');
  });

  it('omits aria-busy when not busy', () => {
    renderWithProviders(
      <PublicPageLayout>
        <p>idle</p>
      </PublicPageLayout>,
    );

    expect(screen.getByRole('main').hasAttribute('aria-busy')).toBe(false);
  });

  it('renders the brand header only when showBrandHeader is true', () => {
    const { rerender } = renderWithProviders(
      <PublicPageLayout>
        <p>x</p>
      </PublicPageLayout>,
    );
    expect(screen.queryByRole('banner')).toBeNull();

    rerender(
      <PublicPageLayout showBrandHeader>
        <p>x</p>
      </PublicPageLayout>,
    );
    expect(screen.getByRole('banner')).toBeDefined();
  });

  it('renders the optional footer when provided', () => {
    renderWithProviders(
      <PublicPageLayout footer={<small>©</small>}>
        <p>main</p>
      </PublicPageLayout>,
    );

    expect(screen.getByRole('contentinfo').textContent).toBe('©');
  });

  it('uses the supplied mainLabel as the accessible name for <main>', () => {
    renderWithProviders(
      <PublicPageLayout mainLabel="Calendar share — Acme">
        <p>x</p>
      </PublicPageLayout>,
    );

    expect(screen.getByRole('main', { name: 'Calendar share — Acme' })).toBeDefined();
  });

  it('passes axe a11y checks with brand header and footer', async () => {
    const { container } = renderWithProviders(
      <PublicPageLayout showBrandHeader footer={<small>footer</small>} mainLabel="example">
        <h1>Heading</h1>
        <p>Body</p>
      </PublicPageLayout>,
    );

    const results = await axe(container);
    expect(results).toHaveNoViolations();
  });
});
