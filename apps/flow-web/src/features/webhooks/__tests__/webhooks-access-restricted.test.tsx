/**
 * A 401/403 on the webhook list reaches the route boundary through the app's
 * `throwOnError: true` QueryClient and renders the access-restricted panel.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../../../lib/api-error';

const mocks = vi.hoisted(() => ({ error: null as unknown }));

vi.mock('@tanstack/react-router', () => ({
  createLazyFileRoute: () => (options: { component: unknown }) => ({ options }),
  getRouteApi: () => ({ useParams: () => ({ id: 'ws-1' }) }),
}));

vi.mock('../../../lib/api', () => ({
  apiRequest: () => Promise.reject(mocks.error),
}));

vi.mock('../../workspaces/access-restricted', () => ({
  default: ({ sectionTitleKey }: { sectionTitleKey: string }) => (
    <div data-testid="access-restricted">{sectionTitleKey}</div>
  ),
}));

vi.mock('../../../components/page-header', () => ({
  default: ({ title }: { title: string }) => <h1>{title}</h1>,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { resolvedLanguage: 'en' } }),
}));

import { Route } from '../../../routes/_authenticated.workspaces.$id.settings.webhooks.lazy';

const RouteComponent = (Route as unknown as { options: { component: () => ReactNode } }).options
  .component;

function renderRoute(): void {
  // Mirrors createQueryClient() in @nodate-flow/sdk: queries throw to the nearest boundary.
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, throwOnError: true } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <RouteComponent />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mocks.error = null;
  vi.spyOn(console, 'error').mockImplementation(() => undefined);
});

describe('webhooks route access', () => {
  it.each([401, 403])('renders AccessRestricted for a %i', async (status) => {
    mocks.error = new ApiError('WS.FORBIDDEN', 'forbidden', status);
    renderRoute();
    await waitFor(() => {
      expect(screen.getByTestId('access-restricted').textContent).toBe('nav.webhooks');
    });
  });
});
