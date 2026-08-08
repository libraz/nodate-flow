// Thin typed client factory wrapping openapi-fetch with the generated
// nodate-flow OpenAPI paths. Consumers call createClient once with a
// base URL and an optional dynamic token provider and get a fully typed
// fetch client back. The token provider is invoked per request so
// consumers can rotate the access token (e.g. after refresh) without
// rebuilding the client.

import createOpenapiFetch, { type Client, type ClientOptions } from 'openapi-fetch';

import type { paths } from './openapi.js';

/**
 * Serialization for array query parameters.
 *
 * Every array parameter in the schema is declared `explode: false`, i.e.
 * one comma-joined occurrence (`?priority=4,2`). The API reads only the
 * first occurrence of a repeated parameter, so the exploded form
 * `?priority=4&priority=2` filters by `4` alone and returns a short list
 * with no error and no warning. openapi-fetch defaults to the exploded
 * form, hence this client-wide default: it belongs here rather than at
 * the call sites, because a rule spelled out per request is a rule that
 * only holds for the requests someone remembered.
 */
const ARRAY_QUERY_SERIALIZER = {
  array: { style: 'form', explode: false },
} as const;

/** Options accepted by createClient. */
export interface CreateClientOptions {
  /** Base URL of the nodate-flow HTTP API, e.g. https://api.example.com. */
  baseUrl: string;
  /**
   * Optional static bearer access token. Prefer {@link tokenProvider}
   * when the token can rotate during the client's lifetime.
   */
  token?: string;
  /**
   * Optional per-request token provider. Invoked on every request; the
   * returned string (if any) is attached as `Authorization: Bearer <token>`.
   * Takes precedence over {@link token}. Send credentials include cookies
   * so the refresh cookie (nd_rt) is forwarded automatically.
   */
  tokenProvider?: () => string | undefined;
  /** Extra fetch options forwarded to openapi-fetch. */
  fetchOptions?: Omit<ClientOptions, 'baseUrl' | 'headers'>;
}

/**
 * createClient returns a typed openapi-fetch client bound to the
 * generated nodate-flow OpenAPI schema.
 */
export function createClient(options: CreateClientOptions): Client<paths> {
  const client = createOpenapiFetch<paths>({
    baseUrl: options.baseUrl,
    credentials: 'include',
    querySerializer: ARRAY_QUERY_SERIALIZER,
    ...options.fetchOptions,
  });
  client.use({
    onRequest({ request }) {
      const t = options.tokenProvider?.() ?? options.token;
      if (t) request.headers.set('Authorization', `Bearer ${t}`);
      return request;
    },
  });
  return client;
}

export type NodateFlowClient = Client<paths>;
