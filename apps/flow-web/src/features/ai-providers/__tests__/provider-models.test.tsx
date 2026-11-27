/**
 * ProviderModels: collapsed sections stay off the network, an open one lists
 * the provider's models, and every change goes out as the documented request
 * and refreshes the caches the picker reads.
 */

import { QueryClient } from '@tanstack/react-query';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { API_URL, server, useMockApi } from '@tests/msw/server';
import { HttpResponse, http } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const toastMocks = vi.hoisted(() => ({ show: vi.fn() }));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: toastMocks.show },
}));

import { authStore } from '../../auth/auth-store';
import { aiProvidersKeys } from '../api';
import { type ProviderModel, providerModelsKeys } from '../models-api';
import ProviderModels from '../provider-models';

useMockApi();

const WS = 'ws-1';
const PROVIDER = 'p-1';
const BASE = `${API_URL}/workspaces/${WS}/ai/providers/${PROVIDER}`;

function seedAuthToken(): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setAccessToken(`${header}.${payload}.`);
}

function model(over: Partial<ProviderModel>): ProviderModel {
  return {
    id: 'm-1',
    name: 'gpt-4o',
    displayName: 'GPT-4o',
    enabled: true,
    isDefault: false,
    agentCount: 0,
    ...over,
  };
}

let models: ProviderModel[] = [];
let listCalls = 0;

function listHandler() {
  return http.get(`${BASE}/models`, () => {
    listCalls += 1;
    return HttpResponse.json({ models });
  });
}

function renderSection(over: { isWorkspaceDefault?: boolean } = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, throwOnError: false }, mutations: { retry: false } },
  });
  const result = renderWithProviders(
    <ProviderModels
      workspaceId={WS}
      providerId={PROVIDER}
      isWorkspaceDefault={over.isWorkspaceDefault ?? false}
    />,
    { queryClient },
  );
  return { ...result, queryClient };
}

async function openSection(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: /providers\.models\.title/ }));
}

beforeEach(() => {
  seedAuthToken();
  models = [];
  listCalls = 0;
  toastMocks.show.mockReset();
});

describe('ProviderModels', () => {
  it('renders collapsed and does not fetch until opened', async () => {
    server.use(listHandler());
    const user = userEvent.setup();
    renderSection();

    const toggle = screen.getByRole('button', { name: /providers\.models\.title/ });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    await new Promise((r) => setTimeout(r, 50));
    expect(listCalls).toBe(0);

    models = [model({})];
    await openSection(user);
    expect(await screen.findByText('gpt-4o')).toBeTruthy();
    expect(listCalls).toBe(1);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
  });

  it('shows the default badge, usage count and disabled state', async () => {
    models = [
      model({ id: 'm-1', name: 'gpt-4o', isDefault: true, agentCount: 2 }),
      model({ id: 'm-2', name: 'gpt-4o-mini', displayName: 'Mini', enabled: false }),
    ];
    server.use(listHandler());
    const user = userEvent.setup();
    renderSection();
    await openSection(user);

    expect(await screen.findByText('providers.models.default_badge')).toBeTruthy();
    expect(screen.getByText('providers.models.in_use')).toBeTruthy();
    expect(screen.getByText('providers.models.disabled')).toBeTruthy();
    expect(screen.queryByText('providers.models.no_default')).toBeNull();
  });

  it('says no default is set when no enabled model is the default', async () => {
    models = [model({ isDefault: false })];
    server.use(listHandler());
    const user = userEvent.setup();
    renderSection();
    await openSection(user);

    expect(await screen.findByText('providers.models.no_default')).toBeTruthy();
  });

  it('shows the workspace default note only for the workspace default provider', async () => {
    models = [model({})];
    server.use(listHandler());
    const user = userEvent.setup();
    renderSection({ isWorkspaceDefault: true });
    await openSection(user);

    expect(await screen.findByText('providers.models.workspace_default_note')).toBeTruthy();
  });

  it('disables a model with PATCH {enabled:false}', async () => {
    models = [model({ id: 'm-1' })];
    let body: unknown;
    server.use(
      listHandler(),
      http.patch(`${BASE}/models/m-1`, async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ ok: true });
      }),
    );
    const user = userEvent.setup();
    renderSection();
    await openSection(user);

    await user.click(
      await screen.findByRole('switch', { name: /providers\.models\.enable_toggle/ }),
    );
    await waitFor(() => {
      expect(body).toEqual({ enabled: false });
    });
  });

  it('toasts the error text of a 409 refusal', async () => {
    models = [model({ id: 'm-1', agentCount: 1 })];
    server.use(
      listHandler(),
      http.patch(`${BASE}/models/m-1`, () =>
        HttpResponse.json(
          {
            type: 'AI.MODEL.IN_USE',
            title: 'Conflict',
            detail: 'The model is used by one or more agents.',
            status: 409,
          },
          { status: 409, headers: { 'Content-Type': 'application/problem+json' } },
        ),
      ),
    );
    const user = userEvent.setup();
    renderSection();
    await openSection(user);

    await user.click(
      await screen.findByRole('switch', { name: /providers\.models\.enable_toggle/ }),
    );
    await waitFor(() => {
      expect(toastMocks.show).toHaveBeenCalledWith({
        tone: 'danger',
        message: 'The model is used by one or more agents.',
      });
    });
  });

  it('sets the default with PUT {modelId} and only offers it on enabled non-default rows', async () => {
    models = [
      model({ id: 'm-1', name: 'gpt-4o', isDefault: true }),
      model({ id: 'm-2', name: 'gpt-4o-mini', enabled: false }),
      model({ id: 'm-3', name: 'o3' }),
    ];
    let body: unknown;
    server.use(
      listHandler(),
      http.put(`${BASE}/default-model`, async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ ok: true });
      }),
    );
    const user = userEvent.setup();
    renderSection();
    await openSection(user);

    const buttons = await screen.findAllByRole('button', { name: 'providers.models.set_default' });
    expect(buttons).toHaveLength(1);
    await user.click(buttons[0] as HTMLElement);
    await waitFor(() => {
      expect(body).toEqual({ modelId: 'm-3' });
    });
  });

  it('invalidates the picker, provider list and models list after adding a model', async () => {
    models = [model({})];
    server.use(
      listHandler(),
      http.get(`${BASE}/available-models`, () => HttpResponse.json({ models: [] })),
      http.post(`${BASE}/models`, () =>
        HttpResponse.json(model({ id: 'm-9', name: 'new-model', displayName: 'new-model' })),
      ),
    );
    const user = userEvent.setup();
    const { queryClient } = renderSection();
    const invalidated = vi.spyOn(queryClient, 'invalidateQueries');
    await openSection(user);
    await screen.findByText('gpt-4o');

    await user.click(screen.getByRole('button', { name: 'providers.models.add' }));
    await user.type(await screen.findByRole('combobox'), 'new-model');
    await user.click(screen.getByRole('button', { name: 'providers.models.add_dialog.submit' }));

    await waitFor(() => {
      const keys = invalidated.mock.calls.map((c) => c[0]?.queryKey);
      expect(keys).toContainEqual(['ai-models', WS]);
      expect(keys).toContainEqual(providerModelsKeys.list(WS, PROVIDER));
      expect(keys).toContainEqual(aiProvidersKeys.list(WS));
    });
  });
});
