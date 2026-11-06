/**
 * Sidebar brand row: deployment name always, logo only when configured.
 */

import { DEFAULT_APP_NAME } from '@nodate-flow/ui/brand';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import type { ReactElement } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to }: { children: unknown; to: string }) => (
    <a href={to}>{children as never}</a>
  ),
  useNavigate: () => vi.fn(),
  useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
    select({ location: { pathname: '/today' } }),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: vi.fn(async () => ({ data: { data: [] } })) },
  authSdk: { GET: vi.fn(async () => ({ data: { data: [] } })) },
}));

function renderSidebar(Sidebar: () => ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <Sidebar />
    </QueryClientProvider>,
  );
}

const RAIL = '[class*="railBrand"]';
const LOGO = '[class*="brandLogo"]';

function mockBrand({ logo = false, icon = false }: { logo?: boolean; icon?: boolean }) {
  vi.doMock('../../../lib/brand', () => ({
    APP_NAME: 'Acme Flow',
    APP_LOGO: logo ? { src: '/logo.svg', darkSrc: undefined } : undefined,
    APP_ICON: icon ? { src: '/icon.svg', darkSrc: undefined } : undefined,
  }));
}

afterEach(() => {
  window.localStorage.removeItem('nf.sidebar-collapsed');
  vi.resetModules();
  vi.doUnmock('../../../lib/brand');
});

describe('Sidebar brand row', () => {
  it('shows the default name and no logo by default', async () => {
    const { default: Sidebar } = await import('../sidebar');
    const { container } = renderSidebar(Sidebar);
    expect(screen.getAllByText(DEFAULT_APP_NAME).length).toBeGreaterThan(0);
    expect(container.querySelector('img')).toBeNull();
    expect(container.querySelector('[data-brand-mark="sm"]')).not.toBeNull();
  });

  it('renders only the logo, named by the app name, when a logo is configured', async () => {
    mockBrand({ logo: true });
    const { default: Sidebar } = await import('../sidebar');
    const { container } = renderSidebar(Sidebar);
    const logo = container.querySelector('img[src="/logo.svg"]');
    expect(logo?.getAttribute('alt')).toBe('Acme Flow');
    expect(container.querySelector('[title="Acme Flow"]')).toBeNull();
    expect(container.querySelector('[data-collapsed]')).toBeNull();
    expect(container.querySelector(RAIL)).not.toBeNull();
    expect(container.querySelector(`${RAIL} [data-brand-mark="sm"]`)).not.toBeNull();
  });

  it('shows the icon plus the name when only an icon is configured', async () => {
    mockBrand({ icon: true });
    const { default: Sidebar } = await import('../sidebar');
    const { container } = renderSidebar(Sidebar);
    expect(container.querySelector('img[src="/icon.svg"]')?.getAttribute('alt')).toBe('');
    expect(screen.getByText('Acme Flow').getAttribute('title')).toBe('Acme Flow');
    expect(container.querySelector(RAIL)).toBeNull();
    expect(container.querySelector('[data-brand-mark]')).toBeNull();
  });

  it('keeps the mark when the desktop rail is collapsed', async () => {
    window.localStorage.setItem('nf.sidebar-collapsed', '1');
    const { default: Sidebar } = await import('../sidebar');
    const { container } = renderSidebar(Sidebar);
    expect(container.querySelector('[data-collapsed]')).not.toBeNull();
    expect(container.querySelector('[data-brand-mark="sm"]')).not.toBeNull();
    expect(container.querySelector(RAIL)).toBeNull();
  });

  it('collapsed with a logo only: the rail brand holds the mark and the hidden name', async () => {
    window.localStorage.setItem('nf.sidebar-collapsed', '1');
    mockBrand({ logo: true });
    const { default: Sidebar } = await import('../sidebar');
    const { container } = renderSidebar(Sidebar);
    const aside = container.querySelector('[data-collapsed]');
    expect(aside?.className).toContain('collapsed');
    expect(container.querySelector(LOGO)).not.toBeNull();
    const rail = container.querySelector(RAIL);
    expect(rail?.querySelector('[data-brand-mark="sm"]')).not.toBeNull();
    expect(rail?.textContent).toBe('Acme Flow');
  });

  it('collapsed with logo and icon: the rail brand holds the icon', async () => {
    window.localStorage.setItem('nf.sidebar-collapsed', '1');
    mockBrand({ logo: true, icon: true });
    const { default: Sidebar } = await import('../sidebar');
    const { container } = renderSidebar(Sidebar);
    expect(container.querySelector('[data-collapsed]')?.className).toContain('collapsed');
    const icon = container.querySelector(`${RAIL} img`);
    expect(icon?.getAttribute('src')).toBe('/icon.svg');
    expect(icon?.getAttribute('alt')).toBe('');
    expect(container.querySelector(`${RAIL} [data-brand-mark]`)).toBeNull();
    expect(container.querySelector(RAIL)?.textContent).toBe('Acme Flow');
  });
});
