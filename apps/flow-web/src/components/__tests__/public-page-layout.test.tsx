/**
 * PublicPageLayout — structural and a11y coverage. Verifies the layout
 * always emits a `<main>` landmark, opt-in regions render only when
 * requested, and the busy flag forwards to `aria-busy` so assistive
 * tech can announce skeleton state.
 */

import { DEFAULT_APP_NAME } from '@nodate-flow/ui/brand';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';
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
    const banner = screen.getByRole('banner');
    expect(banner.textContent).toBe(DEFAULT_APP_NAME);
    expect(banner.querySelector('img')).toBeNull();
    expect(banner.querySelector('[data-brand-mark="sm"]')).not.toBeNull();
  });

  it('shows only the logo, named by the app name, when a logo is configured', async () => {
    vi.resetModules();
    vi.doMock('../../lib/brand', () => ({
      APP_NAME: 'Acme Flow',
      APP_LOGO: { src: '/logo.svg', darkSrc: undefined },
      APP_ICON: undefined,
    }));
    const { default: Layout } = await import('../public-page-layout');
    renderWithProviders(
      <Layout showBrandHeader>
        <p>x</p>
      </Layout>,
    );
    const banner = screen.getByRole('banner');
    const img = banner.querySelector('img');
    expect(img?.getAttribute('src')).toBe('/logo.svg');
    expect(img?.getAttribute('alt')).toBe('Acme Flow');
    expect(banner.querySelector('[data-brand-mark]')).toBeNull();
    expect(banner.textContent).toBe('');
    vi.doUnmock('../../lib/brand');
  });

  it('shows a decorative icon plus the name when only an icon is configured', async () => {
    vi.resetModules();
    vi.doMock('../../lib/brand', () => ({
      APP_NAME: 'Acme Flow',
      APP_LOGO: undefined,
      APP_ICON: { src: '/icon.svg', darkSrc: undefined },
    }));
    const { default: Layout } = await import('../public-page-layout');
    renderWithProviders(
      <Layout showBrandHeader>
        <p>x</p>
      </Layout>,
    );
    const banner = screen.getByRole('banner');
    const img = banner.querySelector('img');
    expect(img?.getAttribute('src')).toBe('/icon.svg');
    expect(img?.getAttribute('alt')).toBe('');
    expect(banner.querySelector('[data-brand-mark]')).toBeNull();
    expect(banner.textContent).toBe('Acme Flow');
    vi.doUnmock('../../lib/brand');
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
