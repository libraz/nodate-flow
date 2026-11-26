import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import CostTodaySummary from '../cost-today-summary';

const mocks = vi.hoisted(() => ({
  role: null as string | null,
  query: {} as Record<string, unknown>,
}));

vi.mock('../../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => mocks.role,
  isWorkspaceAdmin: (role: string | null) => role === 'owner' || role === 'admin',
}));

vi.mock('../cost-api', () => ({
  useAiCostTodayQuery: () => mocks.query,
}));

beforeEach(() => {
  mocks.role = 'admin';
  mocks.query = {
    data: { costUsd: 1.5, date: '2026-11-26' },
    isError: false,
    isLoading: false,
  };
});

describe('CostTodaySummary', () => {
  it('shows the amount and date to an admin', () => {
    renderWithProviders(<CostTodaySummary workspaceId="ws-1" />);

    expect(screen.getByRole('region')).toBeTruthy();
    expect(screen.getByText('$1.50')).toBeTruthy();
    expect(screen.getByText(/2026/)).toBeTruthy();
  });

  it.each(['member', 'guest', null])('renders nothing for role %s', (role) => {
    mocks.role = role;
    const { container } = renderWithProviders(<CostTodaySummary workspaceId="ws-1" />);

    expect(container.textContent).toBe('');
    expect(screen.queryByRole('region')).toBeNull();
  });

  it('renders nothing when the query errored', () => {
    mocks.query = { data: undefined, isError: true, isLoading: false };
    const { container } = renderWithProviders(<CostTodaySummary workspaceId="ws-1" />);

    expect(container.textContent).toBe('');
    expect(screen.queryByRole('region')).toBeNull();
  });
});
