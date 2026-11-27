/**
 * AgentModelSelect: a change goes out as PATCH {modelId}, refreshes the
 * agent list and every provider's model counts, and a model the workspace no
 * longer offers is shown as a disabled option rather than silently replaced.
 */

import { QueryClient } from '@tanstack/react-query';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { API_URL, server, useMockApi } from '@tests/msw/server';
import { HttpResponse, http } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

import { authStore } from '../../auth/auth-store';
import AgentModelSelect from '../agent-model-select';
import { agentsKeys, type ModelSummary } from '../agents-api';

useMockApi();

const WS = 'ws-1';

function seedAuthToken(): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setAccessToken(`${header}.${payload}.`);
}

const MODELS: ModelSummary[] = [
  {
    id: 'm-1',
    name: 'gpt-4o',
    displayName: 'GPT-4o',
    providerId: 'p-1',
    providerKind: 'openai',
    providerName: 'OpenAI',
  },
  {
    id: 'm-2',
    name: 'claude-x',
    displayName: 'Claude X',
    providerId: 'p-2',
    providerKind: 'anthropic',
    providerName: 'Anthropic',
  },
];

function renderSelect(modelId: string, modelName: string) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false, staleTime: Number.POSITIVE_INFINITY },
      mutations: { retry: false },
    },
  });
  queryClient.setQueryData(['ai-models', WS], { total: MODELS.length, models: MODELS });
  const result = renderWithProviders(
    <AgentModelSelect workspaceId={WS} agentId="a-1" modelId={modelId} modelName={modelName} />,
    { queryClient },
  );
  return { ...result, queryClient };
}

beforeEach(() => {
  seedAuthToken();
});

describe('AgentModelSelect', () => {
  it('PATCHes the new model and invalidates agents and provider model lists', async () => {
    let body: unknown;
    server.use(
      http.patch(`${API_URL}/workspaces/${WS}/ai/agents/a-1/model`, async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ ok: true });
      }),
    );
    const user = userEvent.setup();
    const { queryClient } = renderSelect('m-1', 'gpt-4o');
    const invalidated = vi.spyOn(queryClient, 'invalidateQueries');

    await user.selectOptions(screen.getByRole('combobox'), 'm-2');

    await waitFor(() => {
      expect(body).toEqual({ modelId: 'm-2' });
    });
    await waitFor(() => {
      const keys = invalidated.mock.calls.map((c) => c[0]?.queryKey);
      expect(keys).toContainEqual(agentsKeys.list(WS));
      expect(keys).toContainEqual(['ai-provider-models', WS]);
    });
  });

  it('shows an unavailable current model as a disabled first option', () => {
    renderSelect('m-gone', 'retired-model');

    const options = screen.getAllByRole('option') as HTMLOptionElement[];
    expect(options[0]?.textContent).toBe('agents.model.unavailable');
    expect(options[0]?.disabled).toBe(true);
    expect(options[0]?.value).toBe('m-gone');
    expect(options.slice(1).every((o) => !o.disabled)).toBe(true);
  });

  it('does not add an unavailable option when the current model is offered', () => {
    renderSelect('m-1', 'gpt-4o');

    expect(screen.getAllByRole('option')).toHaveLength(MODELS.length);
  });
});
