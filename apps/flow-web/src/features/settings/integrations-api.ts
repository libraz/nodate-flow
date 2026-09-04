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
import { authApiRequest } from '../../lib/api';
import { ApiError } from '../../lib/api-error';

export { ApiError as SettingsApiError };

export type ProviderStatus = components['schemas']['ProviderStatus'];
export type ConnectionSummary = components['schemas']['ConnectionSummary'];
export type IntegrationProviderName = 'github' | 'slack' | 'google_calendar' | 'discord';

export const integrationsKeys = {
  list: ['me', 'integrations'] as const,
};

/** GET /me/integrations — provider catalog + current user's connections. */
export function useIntegrationsQuery(): UseSuspenseQueryResult<ProviderStatus[]> {
  return useSuspenseQuery({
    queryKey: integrationsKeys.list,
    queryFn: async (): Promise<ProviderStatus[]> => {
      const data = await authApiRequest(
        (client) => client.GET('/me/integrations'),
        'Failed to load integrations',
      );
      return data.providers ?? [];
    },
  });
}

export interface ConnectRequest {
  provider: IntegrationProviderName;
  redirectTo?: string;
}
export type ConnectResponse = Pick<
  components['schemas']['ConnectIntegrationOutputBody'],
  'authorizeUrl'
>;

/** POST /me/integrations/{provider}/connect — returns the authorize URL. */
export function useConnectIntegration(): UseMutationResult<
  ConnectResponse,
  ApiError,
  ConnectRequest
> {
  return useMutation<ConnectResponse, ApiError, ConnectRequest>({
    throwOnError: false,
    mutationFn: async ({ provider, redirectTo }): Promise<ConnectResponse> => {
      const data = await authApiRequest(
        (client) =>
          client.POST('/me/integrations/{provider}/connect', {
            params: { path: { provider } },
            body: redirectTo != null ? { redirectTo } : {},
          }),
        'Failed to start connect flow',
      );
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
      await authApiRequest(
        (client) =>
          client.DELETE('/me/integrations/{id}', {
            params: { path: { id } },
          }),
        'Failed to disconnect integration',
      );
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: integrationsKeys.list });
    },
  });
}
