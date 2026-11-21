/**
 * With the app's `throwOnError: true` QueryClient a member's 403 on the
 * provider list stays inline instead of reaching the root boundary.
 */

import { QueryClient } from '@tanstack/react-query';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../../../lib/api-error';

vi.mock('../../../lib/api', () => ({
  apiRequest: () => Promise.reject(new ApiError('WS.FORBIDDEN', 'forbidden', 403)),
}));

import ProviderList from '../provider-list';

beforeEach(() => {
  vi.spyOn(console, 'error').mockImplementation(() => undefined);
});

describe('ProviderList forbidden', () => {
  it('renders the inline forbidden state rather than throwing', async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, throwOnError: true } },
    });
    renderWithProviders(<ProviderList workspaceId="ws-1" />, { queryClient });
    expect(await screen.findByText('providers.error.forbidden')).toBeTruthy();
  });
});
