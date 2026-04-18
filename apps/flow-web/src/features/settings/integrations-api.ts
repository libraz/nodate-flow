/**
 * Settings → integrations feature. Thin Suspense-mode hooks around
 * the /me/integrations endpoints; kept in its own file so the main
 * api.ts does not balloon any further.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';
import { SettingsApiError } from './api';

export type ProviderStatus = components['schemas']['ProviderStatus'];
export type ConnectionSummary = components['schemas']['ConnectionSummary'];
export type IntegrationProviderName = 'github' | 'slack' | 'google_calendar';

export const integrationsKeys = {
  list: ['me', 'integrations'] as const,
};

function toError(err: unknown, fallback: string): SettingsApiError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    return new SettingsApiError(code, message);
  }
  return new SettingsApiError(undefined, fallback);
}

/** GET /me/integrations — provider catalog + current user's connections. */
export function useIntegrationsQuery(): UseSuspenseQueryResult<ProviderStatus[]> {
  return useSuspenseQuery({
    queryKey: integrationsKeys.list,
    queryFn: async (): Promise<ProviderStatus[]> => {
      const { data, error } = await sdk.GET('/me/integrations');
      if (error || !data) throw toError(error, 'Failed to load integrations');
      return data.providers ?? [];
    },
  });
}

export interface ConnectRequest {
  provider: IntegrationProviderName;
  redirectTo?: string;
}
export interface ConnectResponse {
  authorizeUrl: string;
}

/** POST /me/integrations/{provider}/connect — returns the authorize URL. */
export function useConnectIntegration(): UseMutationResult<
  ConnectResponse,
  SettingsApiError,
  ConnectRequest
> {
  return useMutation<ConnectResponse, SettingsApiError, ConnectRequest>({
    throwOnError: false,
    mutationFn: async ({ provider, redirectTo }): Promise<ConnectResponse> => {
      const { data, error } = await sdk.POST('/me/integrations/{provider}/connect', {
        params: { path: { provider } },
        body: redirectTo != null ? { redirectTo } : {},
      });
      if (error || !data) throw toError(error, 'Failed to start connect flow');
      return { authorizeUrl: data.authorizeUrl };
    },
  });
}

/** DELETE /me/integrations/{id} — disconnect a provider. */
export function useDisconnectIntegration(): UseMutationResult<void, SettingsApiError, string> {
  const qc = useQueryClient();
  return useMutation<void, SettingsApiError, string>({
    throwOnError: false,
    mutationFn: async (id: string): Promise<void> => {
      const { error } = await sdk.DELETE('/me/integrations/{id}', {
        params: { path: { id } },
      });
      if (error) throw toError(error, 'Failed to disconnect integration');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: integrationsKeys.list });
    },
  });
}
