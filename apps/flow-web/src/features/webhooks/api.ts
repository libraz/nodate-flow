/**
 * Webhooks feature — typed react-query hooks for the workspace outbound
 * webhook subscription endpoints.
 *
 * Surfaces:
 *   - {@link useWebhooksQuery}            GET    /workspaces/{wsId}/webhooks
 *   - {@link useWebhookQuery}             GET    /workspaces/{wsId}/webhooks/{webhookId}
 *   - {@link useCreateWebhookMutation}    POST   /workspaces/{wsId}/webhooks
 *   - {@link useDeleteWebhookMutation}    DELETE /workspaces/{wsId}/webhooks/{webhookId}
 *   - {@link useToggleWebhookMutation}    PATCH  /workspaces/{wsId}/webhooks/{webhookId}/toggle
 *   - {@link useTestWebhookMutation}      POST   /workspaces/{wsId}/webhooks/{webhookId}/test
 *   - {@link useDeliveriesQuery}          GET    /workspaces/{wsId}/webhooks/{webhookId}/deliveries
 *
 * The list query intentionally uses {@link useQuery} (not the suspense
 * variant) so the page can re-render the table inline (toggle / delete /
 * test) without throwing the whole pane back into the Suspense fallback.
 *
 * Notes on the underlying API surface:
 *   - There is **no update endpoint** for url / description / eventTypes;
 *     to change a subscription a caller must delete and recreate. The UI
 *     deliberately omits an edit affordance.
 *   - There is **no retry endpoint** for failed deliveries. The deliveries
 *     listing is therefore read-only history.
 *   - There is **no rotate-secret endpoint**. The signing secret is fixed
 *     for the life of the subscription and is also returned by the GET
 *     detail endpoint, so the create-time reveal is informational rather
 *     than single-shot.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import type { ApiError } from '../../lib/api-error';

/** WebhookSubscription summary DTO mirrored from the generated SDK. */
export type Webhook = components['schemas']['WebhookSubscriptionDTO'];

/** WebhookSubscription detail DTO — adds the signing `secret`. */
export type WebhookDetail = components['schemas']['WebhookSubscriptionDetailDTO'];

/** WebhookDelivery DTO mirrored from the generated SDK. */
export type WebhookDelivery = components['schemas']['WebhookDeliveryDTO'];

/** Body shape for the create endpoint. */
export type CreateWebhookBody = components['schemas']['CreateWebhookInputBody'];

/** Query key factory for the webhooks feature. */
export const webhooksKeys = {
  all: ['webhooks'] as const,
  list: (wsId: string) => ['webhooks', wsId] as const,
  detail: (wsId: string, webhookId: string) => ['webhooks', 'detail', wsId, webhookId] as const,
  deliveries: (wsId: string, webhookId: string) =>
    ['webhooks', 'deliveries', wsId, webhookId] as const,
};

/** Options for {@link useWebhooksQuery}. */
export interface UseWebhooksQueryOptions {
  limit?: number;
  offset?: number;
}

/**
 * GET /workspaces/{wsId}/webhooks — list webhook subscriptions for the
 * workspace. Returns a non-suspense {@link UseQueryResult} so the page can
 * re-render after mutations without re-throwing into Suspense.
 */
export function useWebhooksQuery(
  wsId: string,
  opts: UseWebhooksQueryOptions = {},
): UseQueryResult<Webhook[], ApiError> {
  const limit = opts.limit ?? 100;
  const offset = opts.offset ?? 0;
  return useQuery<Webhook[], ApiError>({
    queryKey: [...webhooksKeys.list(wsId), limit, offset],
    queryFn: async (): Promise<Webhook[]> => {
      if (!wsId) return [];
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/webhooks', {
            params: { path: { wsId }, query: { limit, offset } },
          }),
        'Failed to load webhooks',
      );
      return data.webhooks ?? [];
    },
  });
}

/**
 * GET /workspaces/{wsId}/webhooks/{webhookId} — fetch one webhook
 * subscription including its signing secret. The query is disabled while
 * `webhookId` is empty so callers can pass an empty string before they
 * have selected a row.
 */
export function useWebhookQuery(
  wsId: string,
  webhookId: string,
): UseQueryResult<WebhookDetail, ApiError> {
  return useQuery<WebhookDetail, ApiError>({
    queryKey: webhooksKeys.detail(wsId, webhookId),
    enabled: wsId !== '' && webhookId !== '',
    queryFn: async (): Promise<WebhookDetail> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/webhooks/{webhookId}', {
            params: { path: { wsId, webhookId } },
          }),
        'Failed to load webhook',
      );
      return data.webhook;
    },
  });
}

/** Arguments for {@link useCreateWebhookMutation}. */
export interface CreateWebhookArgs {
  wsId: string;
  body: CreateWebhookBody;
}

/**
 * POST /workspaces/{wsId}/webhooks — create a webhook subscription. The
 * response includes the signing `secret`; callers may surface it directly
 * (the secret is also retrievable via {@link useWebhookQuery}).
 */
export function useCreateWebhookMutation(): UseMutationResult<
  WebhookDetail,
  ApiError,
  CreateWebhookArgs
> {
  const qc = useQueryClient();
  return useMutation<WebhookDetail, ApiError, CreateWebhookArgs>({
    mutationFn: async ({ wsId, body }): Promise<WebhookDetail> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/webhooks', {
            params: { path: { wsId } },
            body,
          }),
        'Failed to create webhook',
      );
      return data.webhook;
    },
    onSuccess: (_data, { wsId }) => {
      void qc.invalidateQueries({ queryKey: webhooksKeys.list(wsId) });
    },
  });
}

/** Arguments for {@link useDeleteWebhookMutation}. */
export interface DeleteWebhookArgs {
  wsId: string;
  webhookId: string;
}

/**
 * DELETE /workspaces/{wsId}/webhooks/{webhookId} — soft-delete a webhook
 * subscription. Future deliveries stop immediately and the row drops out
 * of the list.
 */
export function useDeleteWebhookMutation(): UseMutationResult<void, ApiError, DeleteWebhookArgs> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, DeleteWebhookArgs>({
    mutationFn: async ({ wsId, webhookId }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/webhooks/{webhookId}', {
            params: { path: { wsId, webhookId } },
          }),
        'Failed to delete webhook',
      );
    },
    onSuccess: (_data, { wsId }) => {
      void qc.invalidateQueries({ queryKey: webhooksKeys.list(wsId) });
    },
  });
}

/** Arguments for {@link useToggleWebhookMutation}. */
export interface ToggleWebhookArgs {
  wsId: string;
  webhookId: string;
  isActive: boolean;
}

/**
 * PATCH /workspaces/{wsId}/webhooks/{webhookId}/toggle — activate or
 * deactivate a webhook subscription. Invalidates both the list and the
 * detail query for the affected webhook.
 */
export function useToggleWebhookMutation(): UseMutationResult<void, ApiError, ToggleWebhookArgs> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, ToggleWebhookArgs>({
    mutationFn: async ({ wsId, webhookId, isActive }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/webhooks/{webhookId}/toggle', {
            params: { path: { wsId, webhookId } },
            body: { isActive },
          }),
        'Failed to toggle webhook',
      );
    },
    onSuccess: (_data, { wsId, webhookId }) => {
      void qc.invalidateQueries({ queryKey: webhooksKeys.list(wsId) });
      void qc.invalidateQueries({
        queryKey: webhooksKeys.detail(wsId, webhookId),
      });
    },
  });
}

/** Arguments for {@link useTestWebhookMutation}. */
export interface TestWebhookArgs {
  wsId: string;
  webhookId: string;
}

/** Result emitted by {@link useTestWebhookMutation}. */
export interface TestWebhookResult {
  deliveryId: string;
}

/**
 * POST /workspaces/{wsId}/webhooks/{webhookId}/test — enqueue a synthetic
 * delivery for the subscription. Resolves with the queued delivery id so
 * callers can surface a toast and watch for it in the deliveries drawer.
 * Invalidates the deliveries cache for the webhook on success.
 */
export function useTestWebhookMutation(): UseMutationResult<
  TestWebhookResult,
  ApiError,
  TestWebhookArgs
> {
  const qc = useQueryClient();
  return useMutation<TestWebhookResult, ApiError, TestWebhookArgs>({
    mutationFn: async ({ wsId, webhookId }): Promise<TestWebhookResult> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/webhooks/{webhookId}/test', {
            params: { path: { wsId, webhookId } },
          }),
        'Failed to send test',
      );
      return { deliveryId: data.deliveryId };
    },
    onSuccess: (_data, { wsId, webhookId }) => {
      void qc.invalidateQueries({
        queryKey: webhooksKeys.deliveries(wsId, webhookId),
      });
    },
  });
}

/** Options for {@link useDeliveriesQuery}. */
export interface UseDeliveriesQueryOptions {
  limit?: number;
  offset?: number;
}

/**
 * GET /workspaces/{wsId}/webhooks/{webhookId}/deliveries — list the
 * delivery history for a webhook subscription. Disabled while
 * `webhookId` is empty so callers can pass an empty string before they
 * have selected a row.
 */
export function useDeliveriesQuery(
  wsId: string,
  webhookId: string,
  opts: UseDeliveriesQueryOptions = {},
): UseQueryResult<WebhookDelivery[], ApiError> {
  const limit = opts.limit ?? 100;
  const offset = opts.offset ?? 0;
  return useQuery<WebhookDelivery[], ApiError>({
    queryKey: [...webhooksKeys.deliveries(wsId, webhookId), limit, offset],
    enabled: wsId !== '' && webhookId !== '',
    queryFn: async (): Promise<WebhookDelivery[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/webhooks/{webhookId}/deliveries', {
            params: { path: { wsId, webhookId }, query: { limit, offset } },
          }),
        'Failed to load deliveries',
      );
      return data.deliveries ?? [];
    },
  });
}
