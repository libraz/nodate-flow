/**
 * ModelAddDialog: suggestions are a shortcut for typing. Registered names are
 * left out regardless of case, whatever is in the field is what gets posted,
 * and a failed lookup never stops an add.
 */

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
import ModelAddDialog from '../model-add-dialog';

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

function renderDialog(registeredNames: string[]) {
  return renderWithProviders(
    <ModelAddDialog
      workspaceId={WS}
      providerId={PROVIDER}
      open
      registeredNames={registeredNames}
      onClose={() => undefined}
    />,
  );
}

function available(models: string[]) {
  return http.get(`${BASE}/available-models`, () => HttpResponse.json({ models }));
}

beforeEach(() => {
  seedAuthToken();
});

describe('ModelAddDialog', () => {
  it('lists suggestions and omits registered names ignoring case', async () => {
    server.use(available(['GPT-4o', 'gpt-4o-mini', 'o3']));
    renderDialog(['gpt-4o']);

    expect(await screen.findByRole('option', { name: 'gpt-4o-mini' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'o3' })).toBeTruthy();
    expect(screen.queryByRole('option', { name: 'GPT-4o' })).toBeNull();
    expect(screen.queryByRole('option', { name: 'gpt-4o' })).toBeNull();
  });

  it('fills the field from a suggestion with the keyboard without submitting', async () => {
    let posted = 0;
    server.use(
      available(['alpha', 'beta']),
      http.post(`${BASE}/models`, () => {
        posted += 1;
        return HttpResponse.json({});
      }),
    );
    const user = userEvent.setup();
    renderDialog([]);
    await screen.findByRole('option', { name: 'beta' });

    const input = screen.getByRole('combobox') as HTMLInputElement;
    await user.click(input);
    await user.keyboard('{ArrowDown}{ArrowDown}{Enter}');

    expect(input.value).toBe('beta');
    expect(posted).toBe(0);
  });

  it('posts the field value as typed even when it is not a suggestion', async () => {
    let body: unknown;
    server.use(
      available(['alpha']),
      http.post(`${BASE}/models`, async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({
          id: 'm-1',
          name: 'my-custom-model',
          displayName: 'my-custom-model',
          enabled: true,
          isDefault: false,
          agentCount: 0,
        });
      }),
    );
    const user = userEvent.setup();
    renderDialog([]);
    await screen.findByRole('option', { name: 'alpha' });

    await user.type(screen.getByRole('combobox'), 'my-custom-model');
    await user.click(screen.getByRole('button', { name: 'providers.models.add_dialog.submit' }));

    await waitFor(() => {
      expect(body).toEqual({ name: 'my-custom-model' });
    });
  });

  it('notes a failed lookup and still allows adding', async () => {
    server.use(
      http.get(`${BASE}/available-models`, () =>
        HttpResponse.json(
          { type: 'AI.PROVIDER.UPSTREAM_AUTH_REJECTED', title: 'Bad gateway', status: 502 },
          { status: 502, headers: { 'Content-Type': 'application/problem+json' } },
        ),
      ),
    );
    const user = userEvent.setup();
    renderDialog([]);

    expect(await screen.findByText('providers.models.add_dialog.suggestions_failed')).toBeTruthy();
    expect(screen.getByRole('status')).toBeTruthy();

    await user.type(screen.getByRole('combobox'), 'typed-by-hand');
    const submit = screen.getByRole('button', {
      name: 'providers.models.add_dialog.submit',
    }) as HTMLButtonElement;
    expect(submit.disabled).toBe(false);
  });

  it('notes an empty suggestion list', async () => {
    server.use(available([]));
    renderDialog([]);

    expect(await screen.findByText('providers.models.add_dialog.suggestions_empty')).toBeTruthy();
  });
});
