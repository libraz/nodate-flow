/**
 * The endpoint a provider talks to was the one configured field this
 * list never rendered, even though the API has always returned it. Two
 * consequences: an admin could not tell which gateway an
 * openai_compat row pointed at, and could not find the kind=openai row
 * carrying a base URL — the row the server refuses to build, which used
 * to take the whole workspace's AI down by shadowing a working provider.
 *
 * Rows created before that check existed are still in the table, so the
 * list has to be able to point at them.
 */

import { QueryClient } from '@tanstack/react-query';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it } from 'vitest';

import { type AiProvider, aiProvidersKeys } from '../api';
import ProviderList from '../provider-list';

const WS = 'ws-1';

function provider(over: Partial<AiProvider>): AiProvider {
  return {
    id: 'p-1',
    name: 'p',
    kind: 'openai_compat',
    apiKeyMasked: 'sk-…abcd',
    createdAt: 0,
    ...over,
  } as AiProvider;
}

function renderList(providers: AiProvider[]) {
  const queryClient = new QueryClient({
    // staleTime keeps the seeded rows from being refetched: without it
    // the query fires at the real SDK and happy-dom aborts the request
    // during teardown, which buries a genuine failure in stack traces.
    defaultOptions: {
      queries: { retry: false, throwOnError: false, staleTime: Number.POSITIVE_INFINITY },
    },
  });
  queryClient.setQueryData(aiProvidersKeys.list(WS), providers);
  return renderWithProviders(<ProviderList workspaceId={WS} />, { queryClient });
}

describe('ProviderList', () => {
  it('shows the endpoint a provider is configured against', () => {
    renderList([provider({ id: 'p-1', name: 'gateway', baseUrl: 'https://proxy.internal/v1' })]);
    expect(screen.getByText('https://proxy.internal/v1')).toBeTruthy();
  });

  it('says nothing about an endpoint when none is configured', () => {
    renderList([provider({ id: 'p-1', name: 'official', kind: 'openai' })]);
    expect(screen.queryByText(/https?:\/\//)).toBeNull();
    expect(screen.queryByText('providers.invalid_base_url')).toBeNull();
  });

  it('flags a kind=openai row that carries a base URL', () => {
    renderList([
      provider({ id: 'p-1', name: 'broken', kind: 'openai', baseUrl: 'https://azure/v1' }),
    ]);
    expect(screen.getByText('providers.invalid_base_url')).toBeTruthy();
  });

  it('does not flag the same base URL under a kind that accepts one', () => {
    renderList([
      provider({ id: 'p-1', name: 'fine', kind: 'openai_compat', baseUrl: 'https://azure/v1' }),
    ]);
    expect(screen.getByText('https://azure/v1')).toBeTruthy();
    expect(screen.queryByText('providers.invalid_base_url')).toBeNull();
  });
});
