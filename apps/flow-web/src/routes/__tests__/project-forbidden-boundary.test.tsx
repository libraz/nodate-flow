/** A 403 from any project child route renders <Forbidden/> through the project layout's errorComponent. */

import { render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { ApiError } from '../../lib/api-error';

vi.mock('../../components/forbidden', () => ({
  default: () => <div data-testid="forbidden" />,
}));

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: { errorComponent: unknown }) => ({
    options,
    useParams: () => ({ id: 'ws-1', projectId: 'p-1' }),
  }),
  Link: ({ children }: { children: ReactNode }) => <a href="/">{children}</a>,
  Outlet: () => null,
  useChildMatches: () => [],
}));

import { Route } from '../_authenticated.workspaces.$id.projects.$projectId';

type ErrorComponent = (props: { error: unknown }) => ReactNode;
const errorComponent = (Route as unknown as { options: { errorComponent: ErrorComponent } }).options
  .errorComponent;

describe('project layout errorComponent', () => {
  it('renders Forbidden for a raw 403 ApiError thrown by a child route query', () => {
    const Component = errorComponent;
    render(<Component error={new ApiError('WS.FORBIDDEN', 'forbidden', 403)} />);
    expect(screen.getByTestId('forbidden')).toBeTruthy();
  });

  it('rethrows other failures to the root boundary', () => {
    const Component = errorComponent;
    const spy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    expect(() => render(<Component error={new ApiError('WS.INTERNAL', 'boom', 500)} />)).toThrow();
    spy.mockRestore();
  });
});
