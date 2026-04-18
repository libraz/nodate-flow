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

import { ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

export { ApiError as SettingsApiError };

export type ProviderStatus = components['schemas']['ProviderStatus'];
export type ConnectionSummary = components['schemas']['ConnectionSummary'];
export type IntegrationProviderName = 'github' | 'slack' | 'google_calendar';

export const integrationsKeys = {
  list: ['me', 'integrations'] as const,
};

/** GET /me/integrations — provider catalog + current user's connections. */
export function useIntegrationsQuery(): UseSuspenseQueryResult<ProviderStatus[]> {
  return useSuspenseQuery({
    queryKey: integrationsKeys.list,
    queryFn: async (): Promise<ProviderStatus[]> => {
      const { data, error } = await sdk.GET('/me/integrations');
      if (error || !data) throw toApiError(error, 'Failed to load integrations');
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
  ApiError,
  ConnectRequest
> {
  return useMutation<ConnectResponse, ApiError, ConnectRequest>({
    throwOnError: false,
    mutationFn: async ({ provider, redirectTo }): Promise<ConnectResponse> => {
      const { data, error } = await sdk.POST('/me/integrations/{provider}/connect', {
        params: { path: { provider } },
        body: redirectTo != null ? { redirectTo } : {},
      });
      if (error || !data) throw toApiError(error, 'Failed to start connect flow');
      return { authorizeUrl: data.authorizeUrl };
    },
  });
}

/** DELETE /me/integrations/{id} — disconnect a provider. */
export function useDisconnectIntegration(): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, string>({
    throwOnError: false,
    mutationFn: async (id: string): Promise<void> => {
      const { error } = await sdk.DELETE('/me/integrations/{id}', {
        params: { path: { id } },
      });
      if (error) throw toApiError(error, 'Failed to disconnect integration');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: integrationsKeys.list });
    },
  });
}
