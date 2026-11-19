import { QueryClient } from '@tanstack/react-query';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { API_URL, server, useMockApi } from '@tests/msw/server';
import { HttpResponse, http } from 'msw';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { authStore } from '../../auth/auth-store';
import EmbeddingSettingsPanel from '../embedding-settings';
import { type EmbeddingSettings, embeddingSettingsKeys } from '../embedding-settings-api';

const WORKSPACE_ID = 'ws-1';
const SETTINGS_URL = `${API_URL}/workspaces/${WORKSPACE_ID}/ai/embedding-settings`;

useMockApi();
function seedAuthToken(): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setAccessToken(`${header}.${payload}.`);
}

beforeEach(seedAuthToken);

afterEach(() => {
  authStore.getState().clearSession();
});

function renderSettings(settings: EmbeddingSettings) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false, staleTime: Number.POSITIVE_INFINITY },
      mutations: { retry: false, throwOnError: false },
    },
  });
  queryClient.setQueryData(embeddingSettingsKeys.settings(WORKSPACE_ID), settings);
  return renderWithProviders(<EmbeddingSettingsPanel workspaceId={WORKSPACE_ID} />, {
    queryClient,
  });
}

function enabledSettings(): EmbeddingSettings {
  return {
    mode: 'inherit',
    status: 'enabled',
    model: 'text-embedding-3-small',
    endpoint: 'https://api.openai.com',
  };
}

describe('EmbeddingSettingsPanel', () => {
  it('shows the effective server provider and describes what task data is sent', () => {
    renderSettings(enabledSettings());

    expect(screen.getByText('text-embedding-3-small')).toBeTruthy();
    expect(screen.getByText('https://api.openai.com')).toBeTruthy();
    expect(screen.getByText('providers.embedding.data_use')).toBeTruthy();
    expect(
      screen
        .getByRole('switch', { name: 'providers.embedding.mode_label' })
        .getAttribute('aria-checked'),
    ).toBe('true');
  });

  it('shows when the inherited server provider is not configured', () => {
    renderSettings({ mode: 'inherit', status: 'unconfigured' });

    expect(screen.getByText('providers.embedding.status.unconfigured')).toBeTruthy();
    expect(screen.queryByText('providers.embedding.model')).toBeNull();
    expect(screen.queryByText(/https?:\/\//)).toBeNull();
  });

  it('saves the workspace opt-out and refreshes the displayed effective state', async () => {
    const user = userEvent.setup();
    let patchBody: unknown;
    server.use(
      http.get(SETTINGS_URL, () => HttpResponse.json(enabledSettings())),
      http.patch(SETTINGS_URL, async ({ request }) => {
        patchBody = await request.json();
        return HttpResponse.json({ mode: 'disabled', status: 'disabled' });
      }),
    );
    const { queryClient } = renderSettings(enabledSettings());

    await user.click(screen.getByRole('switch', { name: 'providers.embedding.mode_label' }));
    await user.click(screen.getByRole('button', { name: 'providers.embedding.save' }));

    await waitFor(() => expect(patchBody).toEqual({ mode: 'disabled' }));
    expect(queryClient.getQueryData(embeddingSettingsKeys.settings(WORKSPACE_ID))).toEqual({
      mode: 'disabled',
      status: 'disabled',
    });
    expect(screen.getByText('providers.embedding.status.disabled')).toBeTruthy();
  });
});
