import { render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@tanstack/react-router', () => ({
  createLazyFileRoute: () => (options: unknown) => ({ options }),
  getRouteApi: () => ({ useParams: () => ({ id: 'ws-1' }) }),
}));

vi.mock('../../features/workspaces/workspace-members-table', () => ({
  default: () => <div data-testid="members-table" />,
}));

vi.mock('../../components/page-header', () => ({
  default: ({ title, description }: { title: string; description: string }) => (
    <header>
      <h1>{title}</h1>
      <p>{description}</p>
    </header>
  ),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import { Route } from '../_authenticated.workspaces.$id.members.lazy';

describe('workspace members page', () => {
  it('has an h1 named after the page and a description above the table', () => {
    const Component = (Route as unknown as { options: { component: () => ReactNode } }).options
      .component;
    render(<Component />);
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('nav.members');
    expect(screen.getByText('workspaces.members.page_description')).toBeTruthy();
    expect(screen.getByTestId('members-table')).toBeTruthy();
  });
});
