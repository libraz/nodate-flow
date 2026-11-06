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

afterEach(() => {
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

  it('renders a decorative logo before the label when configured', async () => {
    vi.doMock('../../../lib/brand', () => ({ APP_NAME: 'Acme Flow', APP_LOGO_URL: '/logo.svg' }));
    const { default: Sidebar } = await import('../sidebar');
    const { container } = renderSidebar(Sidebar);
    const img = container.querySelector('img[alt=""]');
    expect(img?.getAttribute('src')).toBe('/logo.svg');
    expect(screen.getByText('Acme Flow')).toBeDefined();
    expect(container.querySelector('[data-brand-mark]')).toBeNull();
  });

  it('keeps the mark when the desktop rail is collapsed', async () => {
    window.localStorage.setItem('nf.sidebar-collapsed', '1');
    const { default: Sidebar } = await import('../sidebar');
    const { container } = renderSidebar(Sidebar);
    expect(container.querySelector('[data-collapsed]')).not.toBeNull();
    expect(container.querySelector('[data-brand-mark="sm"]')).not.toBeNull();
    window.localStorage.removeItem('nf.sidebar-collapsed');
  });
});
