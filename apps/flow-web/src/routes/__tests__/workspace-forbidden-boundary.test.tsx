/** A 403 for a foreign workspace renders <Forbidden/> through the workspace layout's errorComponent. */

import { QueryClient, QueryClientProvider, useSuspenseQuery } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { Component, type ReactNode, Suspense } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { ApiError } from '../../lib/api-error';

const probe = vi.fn();
vi.mock('../../lib/api', () => ({ authApiProbe: (...args: unknown[]) => probe(...args) }));

vi.mock('../../components/forbidden', () => ({
  default: () => <div data-testid="forbidden" />,
}));

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: Record<string, unknown>) => ({
    options,
    useParams: () => ({ id: 'ws-1' }),
  }),
  Link: ({ children }: { children: ReactNode }) => <a href="/">{children}</a>,
  Navigate: () => null,
  Outlet: () => null,
  useChildMatches: () => [],
}));

import { Route } from '../_authenticated.workspaces.$id';

type Options = {
  errorComponent: (props: { error: unknown }) => ReactNode;
  loader: (ctx: { params: { id: string } }) => Promise<unknown>;
};
const { errorComponent: ErrorComponent, loader } = (Route as unknown as { options: Options })
  .options;

class Boundary extends Component<{ children: ReactNode }, { error: unknown }> {
  override state = { error: null as unknown };
  static getDerivedStateFromError(error: unknown) {
    return { error };
  }
  override render() {
    return this.state.error ? <ErrorComponent error={this.state.error} /> : this.props.children;
  }
}

function Child(): ReactNode {
  useSuspenseQuery({
    queryKey: ['members'],
    queryFn: () => Promise.reject(new ApiError('WS.FORBIDDEN', 'forbidden', 403)),
  });
  return null;
}

describe('workspace layout errorComponent', () => {
  it('renders Forbidden when a child suspense query rejects with 403', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, throwOnError: true } },
    });
    const spy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    render(
      <QueryClientProvider client={client}>
        <Boundary>
          <Suspense fallback={null}>
            <Child />
          </Suspense>
        </Boundary>
      </QueryClientProvider>,
    );
    expect(await screen.findByTestId('forbidden')).toBeTruthy();
    spy.mockRestore();
  });

  it('renders Forbidden for 401 and rethrows other failures', () => {
    render(<ErrorComponent error={new ApiError('WS.UNAUTHENTICATED', 'no', 401)} />);
    expect(screen.getByTestId('forbidden')).toBeTruthy();
    const spy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    expect(() =>
      render(<ErrorComponent error={new ApiError('WS.INTERNAL', 'boom', 500)} />),
    ).toThrow();
    spy.mockRestore();
  });
});

describe('workspace layout loader', () => {
  it('throws a 403 ApiError when the probe answers 403', async () => {
    probe.mockResolvedValueOnce(403);
    await expect(loader({ params: { id: 'ws-1' } })).rejects.toMatchObject({ httpStatus: 403 });
  });

  it('keeps the not-found path and resolves on success', async () => {
    probe.mockResolvedValueOnce(404);
    await expect(loader({ params: { id: 'ws-1' } })).rejects.toMatchObject({
      code: 'WS.WORKSPACE.NOT_FOUND',
    });
    probe.mockResolvedValueOnce(200);
    await expect(loader({ params: { id: 'ws-1' } })).resolves.toBeNull();
  });
});
